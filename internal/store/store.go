package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"chineseCards/internal/model"
)

//go:embed schema.sql
var schemaSQL string

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DB() *sql.DB { return s.db }

func hashStr(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{'|'})
		}
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func normalizedNoteHash(raw string) string {
	return hashStr(strings.ToLower(strings.TrimSpace(raw)))
}

func entryHash(kind model.Kind, chinese, english string) string {
	return hashStr(string(kind), chinese, english)
}

func exampleEntryHash(kind model.Kind, chinese string, sourceEntryID int64) string {
	return hashStr(string(kind), chinese, fmt.Sprintf("%d", sourceEntryID))
}

func cardHash(cardType model.CardType, front, back string) string {
	return hashStr(string(cardType), front, back)
}

type InsertNoteResult struct {
	NoteID   int64
	Inserted bool
}

func (s *Store) InsertNote(ctx context.Context, rawText string, noteDate *time.Time) (InsertNoteResult, error) {
	h := normalizedNoteHash(rawText)
	var dateStr *string
	if noteDate != nil {
		v := noteDate.Format("2006-01-02")
		dateStr = &v
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO notes (raw_text, note_date, hash) VALUES (?, ?, ?) ON CONFLICT(hash) DO NOTHING`,
		rawText, dateStr, h)
	if err != nil {
		return InsertNoteResult{}, fmt.Errorf("insert note: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		id, _ := res.LastInsertId()
		return InsertNoteResult{NoteID: id, Inserted: true}, nil
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM notes WHERE hash = ?`, h).Scan(&id); err != nil {
		return InsertNoteResult{}, fmt.Errorf("lookup existing note: %w", err)
	}
	return InsertNoteResult{NoteID: id, Inserted: false}, nil
}

type InsertEntryResult struct {
	EntryID  int64
	Inserted bool
}

func (s *Store) InsertEntry(ctx context.Context, noteID int64, e model.ParsedEntry) (InsertEntryResult, error) {
	h := entryHash(e.Kind, e.Chinese, e.English)
	var englishCol any
	if e.English == "" {
		englishCol = nil
	} else {
		englishCol = e.English
	}
	var pinyinCol any
	if e.Pinyin != "" {
		pinyinCol = e.Pinyin
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO entries (note_id, kind, chinese, chinese_raw, pinyin, english, hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(hash) DO NOTHING`,
		noteID, string(e.Kind), e.Chinese, e.ChineseRaw, pinyinCol, englishCol, h)
	if err != nil {
		return InsertEntryResult{}, fmt.Errorf("insert entry: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		id, _ := res.LastInsertId()
		return InsertEntryResult{EntryID: id, Inserted: true}, nil
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM entries WHERE hash = ?`, h).Scan(&id); err != nil {
		return InsertEntryResult{}, fmt.Errorf("lookup existing entry: %w", err)
	}
	return InsertEntryResult{EntryID: id, Inserted: false}, nil
}

type InsertCardResult struct {
	CardID   int64
	Inserted bool
}

func (s *Store) InsertCard(ctx context.Context, entryID int64, cardType model.CardType, front, back string, clozeAnswer *string) (InsertCardResult, error) {
	h := cardHash(cardType, front, back)
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO cards (entry_id, card_type, front, back, cloze_answer, hash)
		 VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(hash) DO NOTHING`,
		entryID, string(cardType), front, back, clozeAnswer, h)
	if err != nil {
		return InsertCardResult{}, fmt.Errorf("insert card: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		id, _ := res.LastInsertId()
		return InsertCardResult{CardID: id, Inserted: true}, nil
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM cards WHERE hash = ?`, h).Scan(&id); err != nil {
		return InsertCardResult{}, fmt.Errorf("lookup existing card: %w", err)
	}
	return InsertCardResult{CardID: id, Inserted: false}, nil
}

type CardListRow struct {
	ID         int64
	CardType   model.CardType
	Front      string
	Back       string
	Chinese    string
	Kind       model.Kind
	DueAt      time.Time
	LastReview *time.Time
	Reps       int
}

func (s *Store) ListCards(ctx context.Context, limit, offset int) ([]CardListRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.card_type, c.front, c.back, e.chinese_raw, e.kind, c.due_at, c.last_reviewed, c.repetitions
		FROM cards c JOIN entries e ON e.id = c.entry_id
		ORDER BY c.id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []CardListRow{}
	for rows.Next() {
		var r CardListRow
		var dueStr string
		var lastStr sql.NullString
		var kindStr, typeStr string
		if err := rows.Scan(&r.ID, &typeStr, &r.Front, &r.Back, &r.Chinese, &kindStr, &dueStr, &lastStr, &r.Reps); err != nil {
			return nil, err
		}
		r.CardType = model.CardType(typeStr)
		r.Kind = model.Kind(kindStr)
		if t, err := parseSQLiteTime(dueStr); err == nil {
			r.DueAt = t
		}
		if lastStr.Valid {
			if t, err := parseSQLiteTime(lastStr.String); err == nil {
				r.LastReview = &t
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CountCards(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cards`).Scan(&n)
	return n, err
}

func (s *Store) CountDueCards(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM cards WHERE last_reviewed IS NOT NULL AND due_at <= datetime('now')`).Scan(&n)
	return n, err
}

func (s *Store) CountNewCards(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM cards WHERE last_reviewed IS NULL`).Scan(&n)
	return n, err
}

type EntryRow struct {
	ID                 int64
	NoteID             int64
	Kind               model.Kind
	Chinese            string
	ChineseRaw         string
	Pinyin             string  // empty when NULL
	English            string  // empty when NULL
	SuggestedClozeWord *string // nil when NULL
}

const entryRowCols = `id, note_id, kind, chinese, chinese_raw, pinyin, english, suggested_cloze_word`

func scanEntryRow(rows *sql.Rows) (EntryRow, error) {
	var e EntryRow
	var kindStr string
	var pinyin, english, cloze sql.NullString
	if err := rows.Scan(&e.ID, &e.NoteID, &kindStr, &e.Chinese, &e.ChineseRaw, &pinyin, &english, &cloze); err != nil {
		return EntryRow{}, err
	}
	e.Kind = model.Kind(kindStr)
	if pinyin.Valid {
		e.Pinyin = pinyin.String
	}
	if english.Valid {
		e.English = english.String
	}
	if cloze.Valid {
		v := cloze.String
		e.SuggestedClozeWord = &v
	}
	return e, nil
}

// ListAllEntries returns every entry currently stored. Used by the startup
// backfill to regenerate cards (idempotent via card hash).
func (s *Store) ListAllEntries(ctx context.Context) ([]EntryRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+entryRowCols+` FROM entries`)
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()

	var out []EntryRow
	for rows.Next() {
		e, err := scanEntryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListEntriesByNote returns the entries for one note, in insertion order.
func (s *Store) ListEntriesByNote(ctx context.Context, noteID int64) ([]EntryRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+entryRowCols+` FROM entries WHERE note_id = ? AND source_entry_id IS NULL ORDER BY id ASC`,
		noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EntryRow
	for rows.Next() {
		e, err := scanEntryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EnrichEntryUpdate is the payload of an entry-level enrichment write.
type EnrichEntryUpdate struct {
	EntryID            int64
	Pinyin             string // empty => keep existing
	English            string // empty => keep existing
	SuggestedClozeWord *string
	GrammarNote        *string
	TypoCorrection     *string
	EnrichedAt         time.Time
}

// UpdateEntryEnrichment writes Gemini-supplied fields onto an existing entry.
// Pinyin and English are overwritten only when non-empty (so we don't clobber
// user-provided values with an empty string).
func (s *Store) UpdateEntryEnrichment(ctx context.Context, u EnrichEntryUpdate) error {
	var pinyinArg any
	if u.Pinyin != "" {
		pinyinArg = u.Pinyin
	}
	var englishArg any
	if u.English != "" {
		englishArg = u.English
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE entries
		   SET pinyin = COALESCE(?, pinyin),
		       english = COALESCE(?, english),
		       suggested_cloze_word = ?,
		       grammar_note = ?,
		       typo_correction = ?,
		       enriched_at = ?
		 WHERE id = ?`,
		pinyinArg,
		englishArg,
		nullStr(u.SuggestedClozeWord),
		nullStr(u.GrammarNote),
		nullStr(u.TypoCorrection),
		u.EnrichedAt.UTC().Format("2006-01-02 15:04:05"),
		u.EntryID,
	)
	if err != nil {
		return fmt.Errorf("update entry enrichment: %w", err)
	}
	return nil
}

// InsertExampleSentence creates a Gemini-generated example sentence as a new
// `example_sentence` entry linked back to the source word entry. Idempotent.
func (s *Store) InsertExampleSentence(
	ctx context.Context,
	noteID, sourceEntryID int64,
	chinese, pinyin, english, targetWord string,
	enrichedAt time.Time,
) (int64, bool, error) {
	chineseCanon := strings.TrimRight(strings.TrimSpace(chinese), ".!?。！？")
	h := exampleEntryHash(model.KindExampleSentence, chineseCanon, sourceEntryID)
	var pinyinCol any
	if pinyin != "" {
		pinyinCol = pinyin
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO entries (note_id, source_entry_id, kind, chinese, chinese_raw, pinyin, english, suggested_cloze_word, hash, enriched_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(hash) DO NOTHING`,
		noteID, sourceEntryID, string(model.KindExampleSentence),
		chineseCanon, chinese, pinyinCol, english, targetWord, h,
		enrichedAt.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return 0, false, fmt.Errorf("insert example sentence: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		id, _ := res.LastInsertId()
		return id, true, nil
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM entries WHERE hash = ?`, h).Scan(&id); err != nil {
		return 0, false, err
	}
	return id, false, nil
}

// MarkNoteEnriched stamps a note as having been processed by Gemini.
func (s *Store) MarkNoteEnriched(ctx context.Context, noteID int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE notes SET enriched_at = ? WHERE id = ?`,
		at.UTC().Format("2006-01-02 15:04:05"), noteID)
	return err
}

// NotePending is a note awaiting (or that failed) enrichment.
type NotePending struct {
	ID      int64
	RawText string
}

// ListNotesPendingEnrichment returns notes with enriched_at IS NULL.
func (s *Store) ListNotesPendingEnrichment(ctx context.Context) ([]NotePending, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, raw_text FROM notes WHERE enriched_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotePending
	for rows.Next() {
		var n NotePending
		if err := rows.Scan(&n.ID, &n.RawText); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func nullStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// DayCount is one (date, count) pair for stats histograms.
type DayCount struct {
	Date  time.Time
	Count int
}

// ReviewsByDay returns one DayCount per distinct day in the last `days` days,
// ordered by date ASC. Days with zero reviews are NOT included; the caller
// should fill in gaps.
func (s *Store) ReviewsByDay(ctx context.Context, days int) ([]DayCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT date(reviewed_at) AS d, COUNT(*) AS n
		FROM reviews
		WHERE reviewed_at >= date('now', ?)
		GROUP BY d
		ORDER BY d ASC`,
		fmt.Sprintf("-%d days", days-1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DayCount
	for rows.Next() {
		var dateStr string
		var n int
		if err := rows.Scan(&dateStr, &n); err != nil {
			return nil, err
		}
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		out = append(out, DayCount{Date: t, Count: n})
	}
	return out, rows.Err()
}

// DueByDay returns the number of cards due on each day for the next `days` days.
// Cards already overdue (due_at <= now) are bucketed into today.
func (s *Store) DueByDay(ctx context.Context, days int) ([]DayCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			date(MAX(due_at, datetime('now'))) AS d,
			COUNT(*) AS n
		FROM cards
		WHERE last_reviewed IS NOT NULL
		  AND due_at < date('now', ?)
		GROUP BY d
		ORDER BY d ASC`,
		fmt.Sprintf("+%d days", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DayCount
	for rows.Next() {
		var dateStr string
		var n int
		if err := rows.Scan(&dateStr, &n); err != nil {
			return nil, err
		}
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		out = append(out, DayCount{Date: t, Count: n})
	}
	return out, rows.Err()
}

// ReviewAccuracy returns (Good+Easy)/total across all reviews.
// Returns (0, 0) when there are no reviews.
func (s *Store) ReviewAccuracy(ctx context.Context) (rate float64, total int, err error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN rating >= 4 THEN 1 ELSE 0 END), 0),
			COUNT(*)
		FROM reviews`)
	var good int
	if err := row.Scan(&good, &total); err != nil {
		return 0, 0, err
	}
	if total == 0 {
		return 0, 0, nil
	}
	return float64(good) / float64(total), total, nil
}

// TypoSuggestion is a pending Gemini typo correction for one entry.
type TypoSuggestion struct {
	EntryID    int64
	Kind       model.Kind
	ChineseRaw string
	Suggested  string
}

// ListPendingTypos returns entries that have a Gemini typo_correction set.
func (s *Store) ListPendingTypos(ctx context.Context) ([]TypoSuggestion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, chinese_raw, typo_correction
		 FROM entries WHERE typo_correction IS NOT NULL ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TypoSuggestion
	for rows.Next() {
		var t TypoSuggestion
		var kindStr string
		if err := rows.Scan(&t.EntryID, &kindStr, &t.ChineseRaw, &t.Suggested); err != nil {
			return nil, err
		}
		t.Kind = model.Kind(kindStr)
		out = append(out, t)
	}
	return out, rows.Err()
}

// AcceptTypoCorrection rewrites chinese_raw + chinese + hash on the entry to
// use the suggested form, clears typo_correction, and deletes existing cards
// for the entry so the caller can regenerate them. Returns the entry's new
// (kind, chineseRaw, english, suggested_cloze_word) so the caller can call
// cards.Generate.
func (s *Store) AcceptTypoCorrection(ctx context.Context, entryID int64) (kind model.Kind, chineseRaw, english string, clozeHint *string, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		kindStr    string
		oldEnglish sql.NullString
		typo       sql.NullString
		cloze      sql.NullString
	)
	err = tx.QueryRowContext(ctx,
		`SELECT kind, english, typo_correction, suggested_cloze_word FROM entries WHERE id = ?`,
		entryID).Scan(&kindStr, &oldEnglish, &typo, &cloze)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("load entry: %w", err)
	}
	if !typo.Valid || typo.String == "" {
		return "", "", "", nil, fmt.Errorf("no typo correction pending for entry %d", entryID)
	}

	kind = model.Kind(kindStr)
	chineseRaw = typo.String
	if oldEnglish.Valid {
		english = oldEnglish.String
	}
	if cloze.Valid {
		v := cloze.String
		clozeHint = &v
	}

	// Re-canonicalize the same way the parser does.
	canon := strings.TrimRight(strings.TrimSpace(chineseRaw), ".!?。！？")
	newHash := entryHash(kind, canon, english)

	if _, err = tx.ExecContext(ctx,
		`UPDATE entries
		   SET chinese = ?, chinese_raw = ?, hash = ?, typo_correction = NULL
		 WHERE id = ?`,
		canon, chineseRaw, newHash, entryID,
	); err != nil {
		return "", "", "", nil, fmt.Errorf("update entry: %w", err)
	}

	if _, err = tx.ExecContext(ctx,
		`DELETE FROM cards WHERE entry_id = ?`, entryID,
	); err != nil {
		return "", "", "", nil, fmt.Errorf("delete cards: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return "", "", "", nil, fmt.Errorf("commit: %w", err)
	}
	return kind, chineseRaw, english, clozeHint, nil
}

// DismissTypoCorrection clears the typo_correction without changing chinese_raw.
func (s *Store) DismissTypoCorrection(ctx context.Context, entryID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE entries SET typo_correction = NULL WHERE id = ?`, entryID)
	return err
}

// GetDistractors returns up to n distinct strings drawn from the named column
// of OTHER cards. Used by the multiple-choice review UI to build wrong-option
// buttons. Allowed columns: "front", "back", "cloze_answer".
//
// The current card and the correct answer are excluded (case-insensitive).
func (s *Store) GetDistractors(ctx context.Context, column string, excludeID int64, correct string, n int) ([]string, error) {
	switch column {
	case "front", "back", "cloze_answer":
	default:
		return nil, fmt.Errorf("invalid column: %s", column)
	}
	query := fmt.Sprintf(`SELECT DISTINCT %s FROM cards
		WHERE id != ?
		  AND %s IS NOT NULL AND %s != ''
		  AND LOWER(%s) != LOWER(?)
		ORDER BY RANDOM() LIMIT ?`,
		column, column, column, column)
	rows, err := s.db.QueryContext(ctx, query, excludeID, correct, n)
	if err != nil {
		return nil, fmt.Errorf("get distractors: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0, n)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DistractorChoice is a (text, pinyin) pair for a Chinese-side distractor.
// Used by the review UI to render pinyin under each hanzi choice.
type DistractorChoice struct {
	Text   string
	Pinyin string
}

// GetClozeWordDistractors returns up to n short Chinese word distractors for a
// cloze quiz. Pulls from the front column of OTHER cards whose entries are
// word/phrase/verb kinds — never sentences — so a one-word cloze answer
// doesn't get a whole-sentence "distractor" sitting next to it. Each result
// carries the entry's pinyin so the review page can render reading help
// under each option.
//
// This replaces the previous "distractors from cloze_answer column" path,
// which only returned rows for entries that happened to have a cloze_answer
// set — often a deck of one in personal use, producing always-the-same
// distractor and a two-option quiz.
func (s *Store) GetClozeWordDistractors(ctx context.Context, excludeID int64, correct string, n int) ([]DistractorChoice, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT c.front, COALESCE(e.pinyin, '')
		FROM cards c JOIN entries e ON e.id = c.entry_id
		WHERE c.id != ?
		  AND c.front IS NOT NULL AND c.front != ''
		  AND LOWER(c.front) != LOWER(?)
		  AND e.kind IN ('word', 'phrase', 'verb')
		ORDER BY RANDOM() LIMIT ?`,
		excludeID, correct, n)
	if err != nil {
		return nil, fmt.Errorf("get cloze word distractors: %w", err)
	}
	defer rows.Close()
	out := make([]DistractorChoice, 0, n)
	for rows.Next() {
		var d DistractorChoice
		if err := rows.Scan(&d.Text, &d.Pinyin); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetChineseDistractors is GetDistractors specialised for the Chinese "front"
// column: it joins entries to also surface the entry's pinyin, so the review
// page can render pinyin under each hanzi option. The current card and the
// correct answer are excluded case-insensitively, same as GetDistractors.
func (s *Store) GetChineseDistractors(ctx context.Context, excludeID int64, correct string, n int) ([]DistractorChoice, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT c.front, COALESCE(e.pinyin, '')
		FROM cards c JOIN entries e ON e.id = c.entry_id
		WHERE c.id != ?
		  AND c.front IS NOT NULL AND c.front != ''
		  AND LOWER(c.front) != LOWER(?)
		ORDER BY RANDOM() LIMIT ?`,
		excludeID, correct, n)
	if err != nil {
		return nil, fmt.Errorf("get chinese distractors: %w", err)
	}
	defer rows.Close()
	out := make([]DistractorChoice, 0, n)
	for rows.Next() {
		var d DistractorChoice
		if err := rows.Scan(&d.Text, &d.Pinyin); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteNote removes a note and (via FK cascade) all its entries, cards, and
// reviews. Used by the import handler to roll back when smart-parse fails so
// the user can retry the same paste.
func (s *Store) DeleteNote(ctx context.Context, noteID int64) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE id = ?`, noteID)
	if err != nil {
		return 0, fmt.Errorf("delete note: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// DeleteEntryByCardID deletes the entry that owns this card. Schema cascades
// then remove the card itself, its reviews, and any example_sentence entries
// linked via source_entry_id. Returns the number of entries actually deleted
// (0 if the card didn't exist).
func (s *Store) DeleteEntryByCardID(ctx context.Context, cardID int64) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM entries
		 WHERE id = (SELECT entry_id FROM cards WHERE id = ?)`,
		cardID)
	if err != nil {
		return 0, fmt.Errorf("delete entry: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// DeleteEntriesByCardIDs is the bulk counterpart of DeleteEntryByCardID.
// All deletes run in a single transaction; cascades handle cards, reviews,
// and example_sentence children. Returns the count of entries deleted.
func (s *Store) DeleteEntriesByCardIDs(ctx context.Context, cardIDs []int64) (int, error) {
	if len(cardIDs) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	total := 0
	stmt, err := tx.PrepareContext(ctx,
		`DELETE FROM entries
		 WHERE id = (SELECT entry_id FROM cards WHERE id = ?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for _, id := range cardIDs {
		res, err := stmt.ExecContext(ctx, id)
		if err != nil {
			return total, fmt.Errorf("delete entry for card %d: %w", id, err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}

	if err := tx.Commit(); err != nil {
		return total, fmt.Errorf("commit: %w", err)
	}
	return total, nil
}

// ExampleSentence is one of (possibly many) Gemini-generated example sentences
// attached to a word/phrase/verb entry.
type ExampleSentence struct {
	Chinese    string
	Pinyin     string
	English    string
	TargetWord string
}

// ListExampleSentencesForEntry returns example_sentence entries whose source is
// the given entry. Used at review time to present a word card as a cloze
// (using one of its example sentences) instead of a translation.
func (s *Store) ListExampleSentencesForEntry(ctx context.Context, sourceEntryID int64) ([]ExampleSentence, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT chinese_raw, pinyin, english, suggested_cloze_word
		FROM entries
		WHERE kind = 'example_sentence' AND source_entry_id = ?`, sourceEntryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExampleSentence
	for rows.Next() {
		var es ExampleSentence
		var pinyin, english, target sql.NullString
		if err := rows.Scan(&es.Chinese, &pinyin, &english, &target); err != nil {
			return nil, err
		}
		if pinyin.Valid {
			es.Pinyin = pinyin.String
		}
		if english.Valid {
			es.English = english.String
		}
		if target.Valid {
			es.TargetWord = target.String
		}
		out = append(out, es)
	}
	return out, rows.Err()
}

// GetEntryByID returns the entry that owns this card, including the Gemini-
// enriched fields the review layer needs to pick a presentation mode.
func (s *Store) GetEntryByCardID(ctx context.Context, cardID int64) (*EntryRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+entryRowCols+` FROM entries
		WHERE id = (SELECT entry_id FROM cards WHERE id = ?)`, cardID)

	var e EntryRow
	var kindStr string
	var pinyin, english, cloze sql.NullString
	if err := row.Scan(&e.ID, &e.NoteID, &kindStr, &e.Chinese, &e.ChineseRaw, &pinyin, &english, &cloze); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	e.Kind = model.Kind(kindStr)
	if pinyin.Valid {
		e.Pinyin = pinyin.String
	}
	if english.Valid {
		e.English = english.String
	}
	if cloze.Valid {
		v := cloze.String
		e.SuggestedClozeWord = &v
	}
	return &e, nil
}

// FixLegacyClozeFronts normalizes cards whose `front` still contains the
// `____` placeholder from the pre-refactor schema (when cloze cards stored
// the masked sentence as their front). After the 1-card-per-entry refactor,
// the front should be the FULL Chinese text — rotateBlank inserts the blank
// at render time. Returns the number of cards updated.
func (s *Store) FixLegacyClozeFronts(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.card_type, c.back, e.chinese_raw
		FROM cards c JOIN entries e ON e.id = c.entry_id
		WHERE instr(c.front, '____') > 0`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type row struct {
		id           int64
		cardType     string
		back         string
		newFront     string
	}
	var fixes []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.cardType, &r.back, &r.newFront); err != nil {
			return 0, err
		}
		fixes = append(fixes, r)
	}
	for _, f := range fixes {
		newHash := cardHash(model.CardType(f.cardType), f.newFront, f.back)
		if _, err := s.db.ExecContext(ctx,
			`UPDATE cards SET front = ?, hash = ? WHERE id = ?`,
			f.newFront, newHash, f.id); err != nil {
			return 0, fmt.Errorf("fix card %d: %w", f.id, err)
		}
	}
	return len(fixes), nil
}

// PruneCards enforces the new "one card per entry, no cards for example_sentence
// entries" invariant on existing data. Returns counts for logging.
func (s *Store) PruneCards(ctx context.Context) (deleted int, err error) {
	// Drop all cards attached to example_sentence entries.
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM cards
		WHERE entry_id IN (SELECT id FROM entries WHERE kind = 'example_sentence')`)
	if err != nil {
		return 0, fmt.Errorf("delete example_sentence cards: %w", err)
	}
	n, _ := res.RowsAffected()
	deleted += int(n)

	// For entries with multiple cards, keep the one with the most progress
	// (highest repetitions, then highest ease_factor, then lowest id) and
	// delete the rest. We do this entry-by-entry so we never delete by
	// accident the only card a user has been reviewing.
	rows, err := s.db.QueryContext(ctx, `
		SELECT entry_id FROM cards GROUP BY entry_id HAVING count(*) > 1`)
	if err != nil {
		return deleted, fmt.Errorf("find duplicate-card entries: %w", err)
	}
	defer rows.Close()
	var entryIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return deleted, err
		}
		entryIDs = append(entryIDs, id)
	}

	for _, eid := range entryIDs {
		// Pick the survivor.
		var survivorID int64
		err := s.db.QueryRowContext(ctx, `
			SELECT id FROM cards WHERE entry_id = ?
			ORDER BY repetitions DESC, ease_factor DESC, id ASC LIMIT 1`, eid).Scan(&survivorID)
		if err != nil {
			return deleted, fmt.Errorf("pick survivor for entry %d: %w", eid, err)
		}
		// Delete the rest.
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM cards WHERE entry_id = ? AND id != ?`, eid, survivorID)
		if err != nil {
			return deleted, fmt.Errorf("delete duplicates for entry %d: %w", eid, err)
		}
		n, _ := res.RowsAffected()
		deleted += int(n)
	}

	return deleted, nil
}

// ListCardTypesForEntry returns the set of card_type values that already have
// at least one card for the given entry. Used by backfill to avoid re-emitting
// a card variant that already exists (which would clobber any manual edits).
func (s *Store) ListCardTypesForEntry(ctx context.Context, entryID int64) (map[model.CardType]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT card_type FROM cards WHERE entry_id = ?`, entryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[model.CardType]bool{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out[model.CardType(t)] = true
	}
	return out, rows.Err()
}

// GetIntSetting returns an integer setting by key. Returns (fallback, nil) when
// the key isn't present in the settings table.
func (s *Store) GetIntSetting(ctx context.Context, key string, fallback int) (int, error) {
	var v string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return fallback, nil
	}
	if err != nil {
		return fallback, err
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback, nil
	}
	return n, nil
}

// SetIntSetting upserts an integer setting.
func (s *Store) SetIntSetting(ctx context.Context, key string, value int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, strconv.Itoa(value))
	if err != nil {
		return fmt.Errorf("set int setting: %w", err)
	}
	return nil
}

// SeedIntSettingIfAbsent sets the key to value only if no row exists yet.
// Used to seed defaults from env without overwriting user-set values.
func (s *Store) SeedIntSettingIfAbsent(ctx context.Context, key string, value int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO NOTHING`,
		key, strconv.Itoa(value))
	return err
}

// GetEntryKind returns the entry kind for the entry that owns the given card.
// Returns empty string if the card or its entry doesn't exist.
func (s *Store) GetEntryKind(ctx context.Context, cardID int64) (string, error) {
	var kind string
	err := s.db.QueryRowContext(ctx, `
		SELECT e.kind FROM entries e
		JOIN cards c ON c.entry_id = e.id
		WHERE c.id = ?`, cardID).Scan(&kind)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return kind, err
}

// UpdateCard rewrites front/back/cloze_answer of a card and recomputes its
// hash. Returns an error if the new hash collides with another existing card.
func (s *Store) UpdateCard(ctx context.Context, id int64, front, back string, clozeAnswer *string) error {
	var cardType string
	err := s.db.QueryRowContext(ctx,
		`SELECT card_type FROM cards WHERE id = ?`, id).Scan(&cardType)
	if err != nil {
		return fmt.Errorf("load card: %w", err)
	}
	h := cardHash(model.CardType(cardType), front, back)
	_, err = s.db.ExecContext(ctx,
		`UPDATE cards SET front = ?, back = ?, cloze_answer = ?, hash = ? WHERE id = ?`,
		front, back, clozeAnswer, h, id)
	if err != nil {
		return fmt.Errorf("update card: %w", err)
	}
	return nil
}

// QueueDiagnostic explains why NextCardForReview returned no card.
type QueueDiagnostic struct {
	NewWaiting     int        // cards never reviewed
	DueWaiting     int        // cards reviewed before, due now
	ReviewsToday   int        // total reviews today (new + due)
	NewIntroduced  int        // of ReviewsToday, those where prev_interval=0
	NextDueAt      *time.Time // earliest future-due card, if any
	NextDueFront   string
}

// DiagnoseEmptyQueue returns context for the /review empty state.
func (s *Store) DiagnoseEmptyQueue(ctx context.Context) (*QueueDiagnostic, error) {
	d := &QueueDiagnostic{}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM cards WHERE last_reviewed IS NULL`).Scan(&d.NewWaiting); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM cards WHERE last_reviewed IS NOT NULL AND due_at <= datetime('now')`).
		Scan(&d.DueWaiting); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM reviews WHERE reviewed_at >= date('now')`).
		Scan(&d.ReviewsToday); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM reviews WHERE reviewed_at >= date('now') AND prev_interval = 0`).
		Scan(&d.NewIntroduced); err != nil {
		return nil, err
	}

	var dueStr sql.NullString
	var front sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT due_at, front FROM cards
		WHERE last_reviewed IS NOT NULL AND due_at > datetime('now')
		ORDER BY due_at ASC LIMIT 1`).Scan(&dueStr, &front)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if dueStr.Valid {
		if t, err := parseSQLiteTime(dueStr.String); err == nil {
			d.NextDueAt = &t
		}
		if front.Valid {
			d.NextDueFront = front.String
		}
	}
	return d, nil
}

// IncrementCardLapse bumps a card's lapses counter by 1 without inserting a
// review row or advancing SM-2 state. Used by the in-session retry loop: a
// wrong pick shouldn't consume the daily budget or move the card's schedule,
// but the "I struggled with this" signal is worth keeping.
func (s *Store) IncrementCardLapse(ctx context.Context, cardID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE cards SET lapses = lapses + 1 WHERE id = ?`, cardID)
	if err != nil {
		return fmt.Errorf("increment lapse: %w", err)
	}
	return nil
}

// LastNRatings returns the N most recent review ratings (time descending).
// Used by the motivation picker to detect trailing-correct runs and recovery
// from a lapse without re-computing it from the full reviews log.
func (s *Store) LastNRatings(ctx context.Context, n int) ([]int, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT rating FROM reviews ORDER BY reviewed_at DESC, id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]int, 0, n)
	for rows.Next() {
		var r int
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountReviewsToday returns the number of reviews recorded since midnight
// (local time per SQLite's date('now')). Used by the motivation picker to
// detect milestone hits like "first card of the day" and "every 10th today".
func (s *Store) CountReviewsToday(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM reviews WHERE reviewed_at >= date('now')`).Scan(&n)
	return n, err
}

// ReviewDatesDesc returns distinct review days in descending order, capped at
// `limit`. Used to compute streak.
func (s *Store) ReviewDatesDesc(ctx context.Context, limit int) ([]time.Time, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT date(reviewed_at) AS d FROM reviews ORDER BY d DESC LIMIT ?`,
		limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			continue
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ReviewCard is everything the review handler needs to render a card.
type ReviewCard struct {
	ID           int64
	CardType     model.CardType
	Front        string
	Back         string
	ClozeAnswer  *string
	EaseFactor   float64
	IntervalDays int
	Repetitions  int
	Lapses       int
	DueAt        time.Time
	LastReviewed *time.Time
	IsNew        bool
}

func scanReviewCard(row interface {
	Scan(...any) error
}) (*ReviewCard, error) {
	var c ReviewCard
	var cardType string
	var clozeAnswer sql.NullString
	var dueStr string
	var lastStr sql.NullString
	err := row.Scan(&c.ID, &cardType, &c.Front, &c.Back, &clozeAnswer,
		&c.EaseFactor, &c.IntervalDays, &c.Repetitions, &c.Lapses,
		&dueStr, &lastStr)
	if err != nil {
		return nil, err
	}
	c.CardType = model.CardType(cardType)
	if clozeAnswer.Valid {
		v := clozeAnswer.String
		c.ClozeAnswer = &v
	}
	if t, err := parseSQLiteTime(dueStr); err == nil {
		c.DueAt = t
	}
	if lastStr.Valid {
		if t, err := parseSQLiteTime(lastStr.String); err == nil {
			c.LastReviewed = &t
		}
	}
	c.IsNew = !lastStr.Valid
	return &c, nil
}

const reviewCardCols = `id, card_type, front, back, cloze_answer, ease_factor, interval_days, repetitions, lapses, due_at, last_reviewed`

// NextCardForReview returns the next card the user should see, or nil if no
// card is eligible right now.
//
// `dailyTarget` is the TOTAL daily-review budget — new cards + due-again cards
// combined — so the user gets a steady mix every day instead of "20 brand-new
// cards" or "200 reviews of old cards" depending on deck state. Once the
// budget is spent, the queue returns nil even if more cards are technically
// available; the user can override with /settings/extend or the
// "Review next card anyway" button.
//
// Within the budget, when both kinds are available, the routine prefers
// whichever side is currently under its target share. newTargetRatio (e.g.
// 0.30 = 30%) is the share of the budget aimed at NEW cards. Due cards take
// the rest — keeping SRS schedules timely takes priority on a tie.
func (s *Store) NextCardForReview(ctx context.Context, dailyTarget int, newTargetRatio float64) (*ReviewCard, error) {
	if dailyTarget <= 0 {
		return nil, nil
	}

	var totalToday, newToday int
	if err := s.db.QueryRowContext(ctx,
		`SELECT
		   COUNT(*),
		   COALESCE(SUM(CASE WHEN prev_interval = 0 THEN 1 ELSE 0 END), 0)
		 FROM reviews
		 WHERE reviewed_at >= date('now')`).Scan(&totalToday, &newToday); err != nil {
		return nil, fmt.Errorf("count reviews today: %w", err)
	}
	if totalToday >= dailyTarget {
		return nil, nil
	}

	dueCard, err := s.peekDueCard(ctx)
	if err != nil {
		return nil, err
	}
	newCard, err := s.peekNewCard(ctx)
	if err != nil {
		return nil, err
	}

	// If only one side is available, serve that.
	if dueCard != nil && newCard == nil {
		return dueCard, nil
	}
	if newCard != nil && dueCard == nil {
		return newCard, nil
	}
	if dueCard == nil && newCard == nil {
		return nil, nil
	}

	// Both available — pick by ratio. If we haven't hit the new-card share
	// yet, serve a new one; otherwise serve due. With totalToday==0 we treat
	// the share as 0 so the FIRST card of the day is new whenever newRatio > 0
	// — feels like the user is learning something fresh each session.
	var newShare float64
	if totalToday > 0 {
		newShare = float64(newToday) / float64(totalToday)
	}
	if newShare < newTargetRatio {
		return newCard, nil
	}
	return dueCard, nil
}

func (s *Store) peekDueCard(ctx context.Context) (*ReviewCard, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+reviewCardCols+` FROM cards
		 WHERE last_reviewed IS NOT NULL AND due_at <= datetime('now')
		 ORDER BY due_at ASC LIMIT 1`)
	c, err := scanReviewCard(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

func (s *Store) peekNewCard(ctx context.Context) (*ReviewCard, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+reviewCardCols+` FROM cards
		 WHERE last_reviewed IS NULL ORDER BY id ASC LIMIT 1`)
	c, err := scanReviewCard(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// NextCardEarly returns the soonest-due card, ignoring whether its due_at has
// passed. Used by the "Review anyway" button on the all-caught-up empty state.
func (s *Store) NextCardEarly(ctx context.Context) (*ReviewCard, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+reviewCardCols+` FROM cards
		 ORDER BY (last_reviewed IS NULL) DESC, due_at ASC LIMIT 1`)
	c, err := scanReviewCard(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// GetReviewCard fetches a single card by ID. Returns nil if not found.
func (s *Store) GetReviewCard(ctx context.Context, id int64) (*ReviewCard, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+reviewCardCols+` FROM cards WHERE id = ?`, id)
	c, err := scanReviewCard(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// ApplyReview updates the card's SRS state and inserts a review log row,
// transactionally.
func (s *Store) ApplyReview(
	ctx context.Context,
	cardID int64,
	rating int,
	prevInterval int,
	newInterval int,
	prevEF float64,
	newEF float64,
	repetitions int,
	lapses int,
	dueAt time.Time,
	reviewedAt time.Time,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO reviews (card_id, rating, prev_interval, new_interval, prev_ef, new_ef, reviewed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		cardID, rating, prevInterval, newInterval, prevEF, newEF,
		reviewedAt.UTC().Format("2006-01-02 15:04:05"),
	); err != nil {
		return fmt.Errorf("insert review: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE cards
		   SET ease_factor=?, interval_days=?, repetitions=?, lapses=?, due_at=?, last_reviewed=?
		 WHERE id=?`,
		newEF, newInterval, repetitions, lapses,
		dueAt.UTC().Format("2006-01-02 15:04:05"),
		reviewedAt.UTC().Format("2006-01-02 15:04:05"),
		cardID,
	); err != nil {
		return fmt.Errorf("update card: %w", err)
	}

	return tx.Commit()
}

// ChatMessage is one turn in the tutor-chat thread.
type ChatMessage struct {
	ID        int64
	Role      string // "user" | "assistant"
	Content   string
	CreatedAt time.Time
}

// InsertChatMessage appends a turn to the chat thread.
func (s *Store) InsertChatMessage(ctx context.Context, role, content string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO chat_messages (role, content) VALUES (?, ?)`, role, content)
	if err != nil {
		return 0, fmt.Errorf("insert chat message: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// ListChatMessages returns the whole thread in chronological order.
func (s *Store) ListChatMessages(ctx context.Context) ([]ChatMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, role, content, created_at FROM chat_messages ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list chat messages: %w", err)
	}
	defer rows.Close()
	var out []ChatMessage
	for rows.Next() {
		var m ChatMessage
		var createdStr string
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &createdStr); err != nil {
			return nil, err
		}
		if t, err := parseSQLiteTime(createdStr); err == nil {
			m.CreatedAt = t
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ClearChatMessages wipes the chat thread (irreversible).
func (s *Store) ClearChatMessages(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chat_messages`)
	if err != nil {
		return fmt.Errorf("clear chat messages: %w", err)
	}
	return nil
}

func parseSQLiteTime(s string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02T15:04:05Z",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time format: %q", s)
}
