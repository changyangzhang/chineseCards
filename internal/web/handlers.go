package web

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"chineseCards/internal/cards"
	"chineseCards/internal/config"
	"chineseCards/internal/llm"
	"chineseCards/internal/model"
	"chineseCards/internal/parser"
	"chineseCards/internal/srs"
	"chineseCards/internal/store"
)

type Server struct {
	cfg      config.Config
	store    *store.Store
	renderer *Renderer
	llm      *llm.Client // nil if no API key
}

func NewServer(cfg config.Config, st *store.Store, r *Renderer, lc *llm.Client) *Server {
	return &Server{cfg: cfg, store: st, renderer: r, llm: lc}
}

type homeData struct {
	DueCount   int
	NewCount   int
	TotalCount int
	LLMEnabled bool
	LLMModel   string
	Motivation *Nudge // optional sweet line under today's stats
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	due, _ := s.store.CountDueCards(ctx)
	new_, _ := s.store.CountNewCards(ctx)
	total, _ := s.store.CountCards(ctx)
	// Total reviews-ever for home-nudge phrasing — cheap aggregate, ignore err.
	_, totalReviews, _ := s.store.ReviewAccuracy(ctx)
	s.renderer.Render(w, "home", homeData{
		DueCount:   due,
		NewCount:   new_,
		TotalCount: total,
		LLMEnabled: s.cfg.GeminiAPIKey != "",
		LLMModel:   s.cfg.GeminiModel,
		Motivation: pickHomeNudge(ctx, s.store, totalReviews),
	})
}

type importSummary struct {
	ParsedEntries    int
	NewEntries       int
	DuplicateEntries int
	NewCards         int
	NoteDate         string
	NoteDuplicate    bool
	EnrichmentRan    bool
	EnrichmentError  string
	ExamplesAdded    int
	TyposFlagged     int
}

type importData struct {
	Raw     string
	Summary *importSummary
}

func (s *Server) handleImportGet(w http.ResponseWriter, r *http.Request) {
	s.renderer.Render(w, "import", importData{})
}

// entryState pairs a parsed entry with its DB id and any post-enrichment overrides.
type entryState struct {
	parsed    model.ParsedEntry
	entryID   int64
	finalEng  string  // post-enrichment English (defaults to parsed.English)
	clozeHint *string // post-enrichment cloze target
}

// maxUploadBytes caps file uploads. Gemini's inline-data limit is ~20 MB but
// for personal-deck-sized notes 8 MB is plenty and keeps the round-trip
// snappy.
const maxUploadBytes = 8 << 20

// readUploadedFile pulls a single uploaded file out of an already-parsed
// multipart form, capped at maxUploadBytes. Returns (nil, "", "", nil) when
// no file was sent. mimeType is sniffed when the browser doesn't supply one.
func readUploadedFile(r *http.Request) (data []byte, filename, mimeType string, err error) {
	if r.MultipartForm == nil {
		return nil, "", "", nil
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		return nil, "", "", nil
	}
	header := files[0]
	if header.Size == 0 {
		return nil, "", "", nil
	}
	f, err := header.Open()
	if err != nil {
		return nil, "", "", fmt.Errorf("open upload: %w", err)
	}
	defer f.Close()
	data, err = io.ReadAll(io.LimitReader(f, maxUploadBytes+1))
	if err != nil {
		return nil, "", "", fmt.Errorf("read upload: %w", err)
	}
	if len(data) > maxUploadBytes {
		return nil, "", "", fmt.Errorf("file too large (max %d bytes)", maxUploadBytes)
	}
	mimeType = header.Header.Get("Content-Type")
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = http.DetectContentType(data)
	}
	return data, header.Filename, mimeType, nil
}

// supportedUploadMIME reports whether Gemini can ingest this file directly.
func supportedUploadMIME(m string) bool {
	switch m {
	case "image/png", "image/jpeg", "image/jpg", "image/webp", "image/heic", "image/heif", "application/pdf":
		return true
	}
	return false
}

func (s *Server) handleImportPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// multipart for file uploads; falls back to the urlencoded form for the
	// classic textarea-paste path.
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil && err != http.ErrNotMultipart {
		http.Error(w, "bad form: "+err.Error(), http.StatusBadRequest)
		return
	}

	raw := r.FormValue("raw")

	fileData, fileName, fileMIME, err := readUploadedFile(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// text/* uploads are simpler as raw text — skip the multimodal path.
	if fileData != nil && strings.HasPrefix(fileMIME, "text/") {
		raw = string(fileData)
		fileData = nil
	}

	if raw == "" && fileData == nil {
		http.Error(w, "empty body and no file", http.StatusBadRequest)
		return
	}

	if fileData != nil {
		if s.llm == nil {
			http.Error(w, "file uploads require GEMINI_API_KEY (multimodal parsing). Paste text into the textarea instead.", http.StatusBadRequest)
			return
		}
		if !supportedUploadMIME(fileMIME) {
			http.Error(w, "unsupported file type: "+fileMIME+" (supported: image/png, image/jpeg, image/webp, image/heic, application/pdf, text/*)", http.StatusBadRequest)
			return
		}
	}

	// For text input we use it directly as the note's raw_text. For file
	// uploads we synthesise a stable identifier (name + sha256 + size) so the
	// notes table can dedup re-uploads of the same file via its UNIQUE hash.
	noteText := raw
	if fileData != nil {
		sum := sha256.Sum256(fileData)
		noteText = fmt.Sprintf("FILE_UPLOAD: name=%s mime=%s size=%d sha256=%x", fileName, fileMIME, len(fileData), sum)
	}

	parsed := parser.ParseNotes(raw) // empty raw → empty Entries; still safe

	noteRes, err := s.store.InsertNote(ctx, noteText, parsed.NoteDate)
	if err != nil {
		slog.Error("insert note", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}

	summary := &importSummary{
		NoteDuplicate: !noteRes.Inserted,
	}
	if parsed.NoteDate != nil {
		summary.NoteDate = parsed.NoteDate.Format("2006-01-02")
	}

	// When the LLM is configured AND this is a new note, let Gemini parse
	// directly. For uploaded files this is the ONLY supported path (we don't
	// run local OCR). For text, this also unlocks free-form note parsing.
	var entriesToInsert []model.ParsedEntry
	var llmParseResult *llm.ParseResult
	if noteRes.Inserted && s.llm != nil {
		var pr *llm.ParseResult
		var err error
		if fileData != nil {
			pr, err = s.smartParseFile(ctx, fileData, fileMIME)
		} else {
			pr, err = s.smartParse(ctx, raw)
		}
		if err != nil {
			// Don't insert anything when smart-parse fails — heuristic-parsed
			// entries from free-form notes are usually low quality. Roll back
			// the note row so the user can re-import once Gemini is back.
			summary.EnrichmentError = err.Error()
			slog.Warn("smart parse failed; rolling back note", "err", err, "note_id", noteRes.NoteID)
			if _, derr := s.store.DeleteNote(ctx, noteRes.NoteID); derr != nil {
				slog.Warn("rollback note delete", "err", derr)
			}
			s.renderer.Render(w, "import", importData{Summary: summary})
			return
		}
		llmParseResult = pr
		summary.EnrichmentRan = true
		entriesToInsert = llmEntriesAsParsed(pr.Entries)
	} else {
		entriesToInsert = parsed.Entries
	}
	summary.ParsedEntries = len(entriesToInsert)

	// 1) Insert entries.
	states := make([]entryState, 0, len(entriesToInsert))
	for _, e := range entriesToInsert {
		entRes, err := s.store.InsertEntry(ctx, noteRes.NoteID, e)
		if err != nil {
			slog.Error("insert entry", "err", err, "chinese", e.Chinese)
			continue
		}
		if entRes.Inserted {
			summary.NewEntries++
		} else {
			summary.DuplicateEntries++
		}
		states = append(states, entryState{
			parsed:   e,
			entryID:  entRes.EntryID,
			finalEng: e.English,
		})
	}

	// 2) Apply Gemini-supplied enrichment + example sentences when smartParse
	//    ran. When it didn't (offline / not first import), nothing happens here.
	var exampleStates []entryState
	if llmParseResult != nil {
		exampleStates = s.applySmartEnrichment(ctx, noteRes.NoteID, states, llmParseResult, summary)
	}

	// 3) Generate cards for originals + Gemini-generated example sentences,
	//    using whatever data is now considered "final" for each entry. Under
	//    the 1-card-per-entry model, skip generation when the entry already
	//    has ANY card (regardless of its card_type — covers legacy rows from
	//    the old multi-card schema and any manually-edited cards).
	all := append(states, exampleStates...)
	for _, st := range all {
		existing, err := s.store.ListCardTypesForEntry(ctx, st.entryID)
		if err != nil {
			slog.Warn("list card types", "entry_id", st.entryID, "err", err)
		}
		if len(existing) > 0 {
			continue
		}
		for _, c := range cards.Generate(st.parsed.Kind, st.parsed.ChineseRaw, st.finalEng, st.clozeHint) {
			cardRes, err := s.store.InsertCard(ctx, st.entryID, c.CardType, c.Front, c.Back, c.ClozeAnswer)
			if err != nil {
				slog.Error("insert card", "err", err)
				continue
			}
			if cardRes.Inserted {
				summary.NewCards++
			}
		}
	}

	s.renderer.Render(w, "import", importData{Summary: summary})
}

// handleReviewDelete is invoked from the review page when the user wants to
// drop the card mid-session ("this card isn't worth learning"). Deletes the
// underlying entry (cascade removes the card, its reviews, and any attached
// example sentences) and renders the next card as an HTMX partial — same
// shape as handleReviewPost's response, so the page swaps cleanly.
func (s *Server) handleReviewDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if _, err := s.store.DeleteEntryByCardID(ctx, id); err != nil {
		slog.Error("delete entry by card id", "err", err, "card_id", id)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}

	next, err := s.store.NextCardForReview(ctx, s.dailyTarget(ctx), newCardRatio)
	if err != nil {
		slog.Error("next after delete", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	q, err := s.prepareQuizCard(ctx, next)
	if err != nil {
		slog.Error("prepare next after delete", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	data := quizData{Card: q}
	if q == nil {
		data.Empty = s.buildEmptyState(ctx)
	}
	s.renderer.RenderPartial(w, "review", "card-area", data)
}

// smartParse delegates parsing to Gemini for free-form lesson notes the
// heuristic parser can't handle (tables, prose, parens, B1/B2 phrase lists).
// Returns the structured ParseResult ready for entry insertion.
func (s *Server) smartParse(ctx context.Context, rawText string) (*llm.ParseResult, error) {
	parseCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	return s.llm.ParseAndEnrich(parseCtx, rawText)
}

// smartParseFile is the multimodal sibling of smartParse: Gemini OCRs the
// uploaded image / reads the PDF and produces the same ParseResult shape.
func (s *Server) smartParseFile(ctx context.Context, data []byte, mimeType string) (*llm.ParseResult, error) {
	parseCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	return s.llm.ParseAndEnrichFile(parseCtx, data, mimeType)
}

// llmEntriesAsParsed converts Gemini's parsed-and-enriched entries into the
// model.ParsedEntry shape the rest of the import pipeline expects. Enrichment
// fields (cloze hint, grammar note, typo correction) are NOT carried here —
// they're applied separately via applySmartEnrichment after the entries get
// their database IDs.
func llmEntriesAsParsed(entries []llm.ParsedAndEnrichedEntry) []model.ParsedEntry {
	out := make([]model.ParsedEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, model.ParsedEntry{
			Kind:       model.Kind(e.Kind),
			Chinese:    canonicalChinese(e.Chinese),
			ChineseRaw: e.Chinese,
			Pinyin:     e.Pinyin,
			English:    e.English,
		})
	}
	return out
}

// canonicalChinese mirrors the parser's canonicalization (trim whitespace,
// trim trailing sentence punctuation, lowercase any ASCII letters) so
// smart-parsed entries hash the same way heuristically-parsed ones do.
func canonicalChinese(s string) string {
	return parser.CanonicalChinese(s)
}

// applySmartEnrichment writes Gemini-supplied enrichment fields back to the
// just-inserted entries and creates example_sentence rows linked by Chinese
// text. Returns entryStates for the new example sentences so cards get
// generated for them in the caller's loop.
func (s *Server) applySmartEnrichment(
	ctx context.Context,
	noteID int64,
	states []entryState,
	res *llm.ParseResult,
	summary *importSummary,
) []entryState {
	now := time.Now().UTC()

	// Build a Chinese-canonical → entryState index for cross-referencing
	// example sentences and the LLM's per-entry enrichment data.
	byCanonical := make(map[string]*entryState, len(states))
	for i := range states {
		byCanonical[strings.ToLower(strings.TrimSpace(states[i].parsed.Chinese))] = &states[i]
	}

	for _, en := range res.Entries {
		key := strings.ToLower(strings.TrimSpace(canonicalChinese(en.Chinese)))
		st, ok := byCanonical[key]
		if !ok {
			continue // Gemini emitted an entry we didn't insert (rare)
		}
		if err := s.store.UpdateEntryEnrichment(ctx, store.EnrichEntryUpdate{
			EntryID:            st.entryID,
			Pinyin:             en.Pinyin,
			English:            en.English,
			SuggestedClozeWord: en.SuggestedClozeWord,
			GrammarNote:        en.GrammarNote,
			TypoCorrection:     en.TypoCorrection,
			EnrichedAt:         now,
		}); err != nil {
			slog.Warn("update entry enrichment", "entry_id", st.entryID, "err", err)
			continue
		}
		if en.English != "" {
			st.finalEng = en.English
		}
		st.clozeHint = en.SuggestedClozeWord
		if en.TypoCorrection != nil {
			summary.TyposFlagged++
		}
	}

	out := make([]entryState, 0, len(res.ExampleSentences))
	for _, ex := range res.ExampleSentences {
		parentKey := strings.ToLower(strings.TrimSpace(ex.ParentChinese))
		parent, ok := byCanonical[parentKey]
		if !ok {
			continue
		}
		exID, inserted, err := s.store.InsertExampleSentence(
			ctx, noteID, parent.entryID, ex.Chinese, ex.Pinyin, ex.English, ex.TargetWord, now,
		)
		if err != nil {
			slog.Warn("insert example sentence", "err", err)
			continue
		}
		if inserted {
			summary.ExamplesAdded++
		}
		target := ex.TargetWord
		out = append(out, entryState{
			parsed: model.ParsedEntry{
				Kind:       model.KindExampleSentence,
				ChineseRaw: ex.Chinese,
				Pinyin:     ex.Pinyin,
				English:    ex.English,
			},
			entryID:   exID,
			finalEng:  ex.English,
			clozeHint: &target,
		})
	}

	if err := s.store.MarkNoteEnriched(ctx, noteID, now); err != nil {
		slog.Warn("mark note enriched", "err", err)
	}
	return out
}

// applyEnrichment sends the parsed entries to Gemini, persists per-entry
// updates and any example_sentence entries, and returns entryStates for those
// example sentences so cards can be generated for them. On any failure, the
// note is left with enriched_at = NULL so /admin/enrich-pending can retry.
func (s *Server) applyEnrichment(
	ctx context.Context,
	noteID int64,
	states []entryState,
	summary *importSummary,
) ([]entryState, error) {
	enrichCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	parsedSlice := make([]model.ParsedEntry, len(states))
	for i, st := range states {
		parsedSlice[i] = st.parsed
	}

	res, err := s.llm.Enrich(enrichCtx, parsedSlice)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	// Apply per-entry enrichment.
	for _, en := range res.Entries {
		if en.SourceIndex < 0 || en.SourceIndex >= len(states) {
			continue
		}
		st := &states[en.SourceIndex]
		if err := s.store.UpdateEntryEnrichment(ctx, store.EnrichEntryUpdate{
			EntryID:            st.entryID,
			Pinyin:             en.Pinyin,
			English:            en.English,
			SuggestedClozeWord: en.SuggestedClozeWord,
			GrammarNote:        en.GrammarNote,
			TypoCorrection:     en.TypoCorrection,
			EnrichedAt:         now,
		}); err != nil {
			slog.Warn("update entry enrichment", "entry_id", st.entryID, "err", err)
			continue
		}
		if en.English != "" {
			st.finalEng = en.English
		}
		st.clozeHint = en.SuggestedClozeWord
		if en.TypoCorrection != nil {
			summary.TyposFlagged++
		}
	}

	// Insert example sentences as new entries; return states so cards get
	// generated for them in the caller's main loop.
	out := make([]entryState, 0, len(res.ExampleSentences))
	for _, ex := range res.ExampleSentences {
		if ex.SourceIndex < 0 || ex.SourceIndex >= len(states) {
			continue
		}
		sourceID := states[ex.SourceIndex].entryID
		exID, inserted, err := s.store.InsertExampleSentence(
			ctx, noteID, sourceID, ex.Chinese, ex.Pinyin, ex.English, ex.TargetWord, now,
		)
		if err != nil {
			slog.Warn("insert example sentence", "err", err)
			continue
		}
		if inserted {
			summary.ExamplesAdded++
		}
		target := ex.TargetWord
		out = append(out, entryState{
			parsed: model.ParsedEntry{
				Kind:       model.KindExampleSentence,
				ChineseRaw: ex.Chinese,
				Pinyin:     ex.Pinyin,
				English:    ex.English,
			},
			entryID:   exID,
			finalEng:  ex.English,
			clozeHint: &target,
		})
	}

	if err := s.store.MarkNoteEnriched(ctx, noteID, now); err != nil {
		slog.Warn("mark note enriched", "err", err)
	}
	return out, nil
}

func (s *Server) handleAdminEnrichPending(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if s.llm == nil {
		http.Error(w, "GEMINI_API_KEY not configured", http.StatusServiceUnavailable)
		return
	}

	notes, err := s.store.ListNotesPendingEnrichment(ctx)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}

	processed := 0
	failed := 0
	for _, n := range notes {
		entries, err := s.store.ListEntriesByNote(ctx, n.ID)
		if err != nil {
			slog.Warn("list entries", "note_id", n.ID, "err", err)
			failed++
			continue
		}
		states := make([]entryState, len(entries))
		for i, e := range entries {
			states[i] = entryState{
				parsed: model.ParsedEntry{
					Kind:       e.Kind,
					Chinese:    e.Chinese,
					ChineseRaw: e.ChineseRaw,
					Pinyin:     e.Pinyin,
					English:    e.English,
				},
				entryID:  e.ID,
				finalEng: e.English,
			}
		}

		summary := &importSummary{}
		ex, err := s.applyEnrichment(ctx, n.ID, states, summary)
		if err != nil {
			slog.Warn("retry enrichment", "note_id", n.ID, "err", err)
			failed++
			continue
		}

		// Regenerate cards (idempotent) for originals + new examples.
		all := append(states, ex...)
		for _, st := range all {
			for _, c := range cards.Generate(st.parsed.Kind, st.parsed.ChineseRaw, st.finalEng, st.clozeHint) {
				_, _ = s.store.InsertCard(ctx, st.entryID, c.CardType, c.Front, c.Back, c.ClozeAnswer)
			}
		}
		processed++
	}

	fmt.Fprintf(w, "enrich-pending: %d processed, %d failed, %d still pending\n",
		processed, failed, len(notes)-processed-failed)
}

type cardRow struct {
	ID     int64
	Front  string
	Back   string
	Kind   string
	Reps   int
	DueRel string
}

type cardsData struct {
	Rows  []cardRow
	Total int
	Typos []store.TypoSuggestion
}

func (s *Server) handleCardsList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.store.ListCards(ctx, 200, 0)
	if err != nil {
		slog.Error("list cards", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	total, _ := s.store.CountCards(ctx)

	out := make([]cardRow, 0, len(rows))
	now := time.Now()
	for _, r := range rows {
		out = append(out, cardRow{
			ID:     r.ID,
			Front:  r.Front,
			Back:   r.Back,
			Kind:   string(r.Kind),
			Reps:   r.Reps,
			DueRel: relTime(r.DueAt, now, r.LastReview == nil),
		})
	}
	typos, _ := s.store.ListPendingTypos(ctx)
	s.renderer.Render(w, "cards", cardsData{Rows: out, Total: total, Typos: typos})
}

type settingsData struct {
	NewPerDay     int
	Saved         bool
	NewIntroduced int
	LLMModel      string
	LLMEnabled    bool
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d, _ := s.store.DiagnoseEmptyQueue(ctx)
	data := settingsData{
		NewPerDay:  s.dailyTarget(ctx),
		Saved:      r.URL.Query().Get("saved") == "1",
		LLMEnabled: s.cfg.GeminiAPIKey != "",
		LLMModel:   s.cfg.GeminiModel,
	}
	if d != nil {
		data.NewIntroduced = d.NewIntroduced
	}
	s.renderer.Render(w, "settings", data)
}

func (s *Server) handleSettingsPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	v, err := strconv.Atoi(strings.TrimSpace(r.FormValue("new_per_day")))
	if err != nil || v < 0 || v > 1000 {
		http.Error(w, "new_per_day must be 0..1000", http.StatusBadRequest)
		return
	}
	if err := s.store.SetIntSetting(ctx, "new_per_day", v); err != nil {
		slog.Error("save setting", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	slog.Info("new_per_day updated", "value", v)
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// handleSettingsExtend bumps new_per_day by the requested amount and sends the
// user back to /review. Used by the "Practice N more" button on the empty
// state.
func (s *Server) handleSettingsExtend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	delta, err := strconv.Atoi(strings.TrimSpace(r.FormValue("n")))
	if err != nil || delta <= 0 || delta > 100 {
		delta = 5
	}
	cur := s.dailyTarget(ctx)
	if err := s.store.SetIntSetting(ctx, "new_per_day", cur+delta); err != nil {
		slog.Error("extend setting", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	slog.Info("new_per_day extended", "old", cur, "new", cur+delta)
	http.Redirect(w, r, "/review", http.StatusSeeOther)
}

func (s *Server) handleCardsBulkDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	raw := r.Form["card_ids"]
	if len(raw) == 0 {
		http.Redirect(w, r, "/cards", http.StatusSeeOther)
		return
	}
	ids := make([]int64, 0, len(raw))
	for _, s := range raw {
		v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err == nil {
			ids = append(ids, v)
		}
	}
	n, err := s.store.DeleteEntriesByCardIDs(ctx, ids)
	if err != nil {
		slog.Error("bulk delete", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	slog.Info("bulk delete", "deleted_entries", n, "requested", len(ids))
	http.Redirect(w, r, "/cards", http.StatusSeeOther)
}

func (s *Server) handleCardDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	n, err := s.store.DeleteEntryByCardID(ctx, id)
	if err != nil {
		slog.Error("delete card", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	if n == 0 {
		http.NotFound(w, r)
		return
	}
	slog.Info("card deleted", "card_id", id)
	http.Redirect(w, r, "/cards", http.StatusSeeOther)
}

func (s *Server) handleTypoAccept(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	kind, chineseRaw, english, clozeHint, err := s.store.AcceptTypoCorrection(ctx, id)
	if err != nil {
		slog.Error("accept typo", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for _, c := range cards.Generate(kind, chineseRaw, english, clozeHint) {
		if _, err := s.store.InsertCard(ctx, id, c.CardType, c.Front, c.Back, c.ClozeAnswer); err != nil {
			slog.Warn("regen card after typo accept", "err", err)
		}
	}
	http.Redirect(w, r, "/cards", http.StatusSeeOther)
}

func (s *Server) handleTypoDismiss(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := s.store.DismissTypoCorrection(ctx, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/cards", http.StatusSeeOther)
}

// quizMode is the presentation chosen for THIS render of a card. The same
// card may be reviewed multiple times in different modes — SM-2 state is per
// card, not per mode.
type quizMode string

const (
	ModeMCTranslate    quizMode = "mc_translate"     // Chinese prompt → English answer
	ModeMCTranslateRev quizMode = "mc_translate_rev" // English prompt → Chinese answer
	ModeMCCloze        quizMode = "mc_cloze"         // sentence with blank → Chinese word
)

// quizCard is the per-render state of a review card.
type quizCard struct {
	ID               int64
	Mode             quizMode
	Front            string   // shown to the user
	FrontPinyin      string   // pinyin under the front when it's hanzi; "" otherwise
	Correct          string   // grading answer for THIS render
	Choices          []string // shuffled options including the correct answer
	ChoicesPinyin    []string // pinyin under each choice (parallel to Choices); entries "" when N/A
	HintBelow        string   // optional sub-hint (e.g. English for cloze)
	IsCloze          bool     // affects styling (monospace, gold border)
	IsTranslate      bool
	IsFrontChinese   bool // speaker button on front speaks Chinese
	IsChoicesChinese bool // speaker buttons next to choices speak Chinese
}

// lastResult drives the green/red ribbon shown above the new card after a pick.
type lastResult struct {
	Mode       quizMode
	Front      string
	Correct    string
	Chosen     string
	WasCorrect bool
	IsCloze    bool
	IsReverse  bool   // English → Chinese translation
	Filled     string // for cloze: Front with ____ replaced by Correct
	HintBelow  string
	Motivation *Nudge // optional sweet line shown next to the ribbon
}

type emptyState struct {
	NewWaiting    int    // cards never reviewed
	DueWaiting    int    // cards reviewed before, due now
	ReviewsToday  int    // total reviews today (new + due)
	NewIntroduced int    // of ReviewsToday, the new ones
	DailyTarget   int    // total cap for new + due combined
	NextDueRel    string // "in 18h" / "in 3d" for the soonest future-due card
	NextDueFront  string
	Motivation    *Nudge // warm sign-off shown when the daily target was reached
}

type quizData struct {
	Card  *quizCard
	Last  *lastResult
	Empty *emptyState
}

func (s *Server) prepareQuizCard(ctx context.Context, card *store.ReviewCard) (*quizCard, error) {
	if card == nil {
		return nil, nil
	}
	entry, err := s.store.GetEntryByCardID(ctx, card.ID)
	if err != nil {
		return nil, fmt.Errorf("load entry for card %d: %w", card.ID, err)
	}
	if entry == nil {
		return nil, nil
	}

	// What modes are available for this entry?
	hasEnglish := card.Back != ""

	// Find a sentence we can present as cloze, if any. clozePinyin holds the
	// toned pinyin for that sentence (DB-supplied when the LLM enriched it,
	// computed at render time otherwise).
	var clozeSentence, clozeEnglish, clozePinyin string
	switch entry.Kind {
	case model.KindSentence, model.KindSentenceUntranslated:
		clozeSentence = card.Front // the sentence itself
		clozeEnglish = card.Back   // may be empty
		clozePinyin = pinyinOr(entry.Pinyin, clozeSentence)
	case model.KindWord, model.KindPhrase, model.KindVerb:
		examples, _ := s.store.ListExampleSentencesForEntry(ctx, entry.ID)
		if len(examples) > 0 {
			ex := examples[rand.IntN(len(examples))]
			clozeSentence = ex.Chinese
			clozeEnglish = ex.English
			clozePinyin = pinyinOr(ex.Pinyin, ex.Chinese)
		}
	}

	// If cloze is available, verify there's at least one non-stopword to blank.
	clozeAvailable := false
	if clozeSentence != "" {
		_, ans := rotateBlank(clozeSentence)
		if ans != "" {
			clozeAvailable = true
		}
	}

	var modes []quizMode
	if hasEnglish {
		// Both translate directions are usable when we have English.
		modes = append(modes, ModeMCTranslate, ModeMCTranslateRev)
	}
	if clozeAvailable {
		modes = append(modes, ModeMCCloze)
	}
	if len(modes) == 0 {
		return nil, nil
	}
	mode := modes[rand.IntN(len(modes))]

	q := &quizCard{ID: card.ID, Mode: mode}
	entryPinyin := pinyinOr(entry.Pinyin, card.Front)

	switch mode {
	case ModeMCTranslate:
		q.Front = card.Front  // Chinese
		q.Correct = card.Back // English
		q.IsTranslate = true
		q.IsFrontChinese = true
		q.FrontPinyin = entryPinyin
		// Choices are English (back column) — no pinyin to surface.
		distractors, err := s.store.GetDistractors(ctx, "back", card.ID, q.Correct, 3)
		if err != nil {
			return nil, err
		}
		q.Choices = buildChoices(q.Correct, distractors)
		q.ChoicesPinyin = make([]string, len(q.Choices)) // all "" by length
	case ModeMCTranslateRev:
		q.Front = card.Back    // English shown
		q.Correct = card.Front // Chinese expected
		q.IsTranslate = true
		q.IsChoicesChinese = true
		// Choices are Chinese — fetch pinyin alongside.
		distractors, err := s.store.GetChineseDistractors(ctx, card.ID, q.Correct, 3)
		if err != nil {
			return nil, err
		}
		q.Choices, q.ChoicesPinyin = buildChinChoices(q.Correct, entryPinyin, distractors)
	case ModeMCCloze:
		q.Front, q.Correct = rotateBlank(clozeSentence)
		q.IsCloze = true
		q.IsFrontChinese = true
		q.IsChoicesChinese = true
		q.FrontPinyin = pinyinOr(clozePinyin, q.Front)
		if clozeEnglish != "" {
			q.HintBelow = clozeEnglish
		}
		// Cloze choices are individual words — we don't have per-word pinyin
		// stored, so fall back to the offline library per choice.
		distractors, err := s.store.GetDistractors(ctx, "cloze_answer", card.ID, q.Correct, 3)
		if err != nil {
			return nil, err
		}
		q.Choices = buildChoices(q.Correct, distractors)
		q.ChoicesPinyin = make([]string, len(q.Choices))
		for i, c := range q.Choices {
			q.ChoicesPinyin[i] = pinyinFor(c)
		}
	}
	return q, nil
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	early := r.URL.Query().Get("early") == "1"

	var card *store.ReviewCard
	var err error
	if early {
		card, err = s.store.NextCardEarly(ctx)
	} else {
		card, err = s.store.NextCardForReview(ctx, s.dailyTarget(ctx), newCardRatio)
	}
	if err != nil {
		slog.Error("next card", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	q, err := s.prepareQuizCard(ctx, card)
	if err != nil {
		slog.Error("prepare quiz card", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	data := quizData{Card: q}
	if q == nil {
		data.Empty = s.buildEmptyState(ctx)
	}
	s.renderer.Render(w, "review", data)
}

// newCardRatio: of the daily review budget, what share is aimed at brand-new
// cards. The rest goes to due (already-seen) cards. Hardcoded for now —
// callers don't expose it. Adjust if the deck balance feels off.
const newCardRatio = 0.30

// dailyTarget reads the current daily-review budget (new + due combined) from
// the settings table, falling back to the env-seeded value on error. The
// setting key is "new_per_day" for back-compat with earlier versions where
// the cap only applied to new cards.
func (s *Server) dailyTarget(ctx context.Context) int {
	n, err := s.store.GetIntSetting(ctx, "new_per_day", s.cfg.NewPerDay)
	if err != nil {
		slog.Warn("read daily target setting", "err", err)
		return s.cfg.NewPerDay
	}
	return n
}

func (s *Server) buildEmptyState(ctx context.Context) *emptyState {
	d, err := s.store.DiagnoseEmptyQueue(ctx)
	if err != nil {
		slog.Warn("diagnose empty queue", "err", err)
		return &emptyState{DailyTarget: s.dailyTarget(ctx)}
	}
	es := &emptyState{
		NewWaiting:    d.NewWaiting,
		DueWaiting:    d.DueWaiting,
		ReviewsToday:  d.ReviewsToday,
		NewIntroduced: d.NewIntroduced,
		DailyTarget:   s.dailyTarget(ctx),
		NextDueFront:  d.NextDueFront,
	}
	if d.NextDueAt != nil {
		es.NextDueRel = humanizeDelta(time.Until(*d.NextDueAt))
	}
	es.Motivation = pickEmptyStateNudge(es)
	return es
}

func humanizeDelta(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	if d < time.Hour {
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("in %dh", int(d.Hours()))
	}
	return fmt.Sprintf("in %d days", int(d.Hours()/24))
}

func (s *Server) handleReviewPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	chosen := strings.TrimSpace(r.FormValue("choice"))
	correct := strings.TrimSpace(r.FormValue("correct"))
	frontShown := r.FormValue("front")
	mode := quizMode(r.FormValue("mode"))
	hint := r.FormValue("hint")
	if chosen == "" || correct == "" {
		http.Error(w, "missing choice or correct", http.StatusBadRequest)
		return
	}

	card, err := s.store.GetReviewCard(ctx, id)
	if err != nil {
		slog.Error("get card", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	if card == nil {
		http.Error(w, "card not found", http.StatusNotFound)
		return
	}

	wasCorrect := strings.EqualFold(chosen, correct)
	rating := srs.RatingGood
	if !wasCorrect {
		rating = srs.RatingAgain
	}

	now := time.Now().UTC()
	prevState := srs.State{
		EaseFactor:  card.EaseFactor,
		Interval:    card.IntervalDays,
		Repetitions: card.Repetitions,
		Lapses:      card.Lapses,
		DueAt:       card.DueAt,
	}
	if card.LastReviewed != nil {
		prevState.LastReviewed = *card.LastReviewed
	}
	newState := srs.Apply(prevState, rating, now)

	if err := s.store.ApplyReview(ctx, card.ID, rating,
		prevState.Interval, newState.Interval,
		prevState.EaseFactor, newState.EaseFactor,
		newState.Repetitions, newState.Lapses,
		newState.DueAt, now,
	); err != nil {
		slog.Error("apply review", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}

	last := &lastResult{
		Mode:       mode,
		Front:      frontShown,
		Correct:    correct,
		Chosen:     chosen,
		WasCorrect: wasCorrect,
		IsCloze:    mode == ModeMCCloze,
		IsReverse:  mode == ModeMCTranslateRev,
		HintBelow:  hint,
	}
	if last.IsCloze {
		last.Filled = strings.Replace(frontShown, "____", correct, 1)
	}
	last.Motivation = pickReviewNudge(ctx, s.store, wasCorrect)

	next, err := s.store.NextCardForReview(ctx, s.dailyTarget(ctx), newCardRatio)
	if err != nil {
		slog.Error("next after review", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	q, err := s.prepareQuizCard(ctx, next)
	if err != nil {
		slog.Error("prepare next", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	data := quizData{Card: q, Last: last}
	if q == nil {
		data.Empty = s.buildEmptyState(ctx)
	}
	s.renderer.RenderPartial(w, "review", "card-area", data)
}

// --- Stats ---

type chartBar struct {
	BarX, BarY, BarW, BarH float64
	LabelX, LabelY         float64
	ValueX, ValueY         float64
	Label                  string
	Count                  int
	IsToday                bool
	IsWeekend              bool
}

type chartData struct {
	Width, Height float64
	Bars          []chartBar
	Max           int
}

type statsData struct {
	HasData      bool
	Streak       int
	LastReview   string
	TotalReviews int
	AccuracyPct  int

	ReviewsChart    chartData
	DueChart        chartData
	ReviewsTotal30d int
	DueTotal14d     int
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	reviews, _ := s.store.ReviewsByDay(ctx, 30)
	dueRows, _ := s.store.DueByDay(ctx, 14)
	accuracy, total, _ := s.store.ReviewAccuracy(ctx)
	dates, _ := s.store.ReviewDatesDesc(ctx, 90)

	streak, lastReview := computeStreak(dates)

	reviewsChart := buildChart(reviews, today.AddDate(0, 0, -29), 30, today, 720, 180)
	dueChart := buildChart(dueRows, today, 14, today, 720, 180)

	data := statsData{
		HasData:         total > 0,
		Streak:          streak,
		TotalReviews:    total,
		AccuracyPct:     int(accuracy*100 + 0.5),
		ReviewsChart:    reviewsChart,
		DueChart:        dueChart,
		ReviewsTotal30d: sumBars(reviewsChart.Bars),
		DueTotal14d:     sumBars(dueChart.Bars),
	}
	if !lastReview.IsZero() {
		data.LastReview = lastReview.Format("2006-01-02")
	}
	s.renderer.Render(w, "stats", data)
}

func computeStreak(datesDesc []time.Time) (int, time.Time) {
	if len(datesDesc) == 0 {
		return 0, time.Time{}
	}
	last := datesDesc[0]
	streak := 1
	cursor := last
	for i := 1; i < len(datesDesc); i++ {
		expected := cursor.AddDate(0, 0, -1)
		if datesDesc[i].Equal(expected) {
			streak++
			cursor = expected
			continue
		}
		break
	}
	return streak, last
}

// buildChart turns a sparse []DayCount into a list of SVG-ready bars covering
// exactly `n` consecutive days starting at `start`.
func buildChart(data []store.DayCount, start time.Time, n int, today time.Time, width, height float64) chartData {
	byDate := make(map[string]int, len(data))
	for _, d := range data {
		byDate[d.Date.Format("2006-01-02")] = d.Count
	}

	max := 0
	for i := 0; i < n; i++ {
		d := start.AddDate(0, 0, i)
		if c := byDate[d.Format("2006-01-02")]; c > max {
			max = c
		}
	}

	const labelArea = 22.0
	const topPad = 14.0
	chartH := height - labelArea - topPad
	gap := 3.0
	if n > 20 {
		gap = 2.0
	}
	barW := (width - gap*float64(n-1)) / float64(n)
	if barW < 1 {
		barW = 1
	}

	todayKey := today.Format("2006-01-02")

	bars := make([]chartBar, n)
	for i := 0; i < n; i++ {
		d := start.AddDate(0, 0, i)
		key := d.Format("2006-01-02")
		count := byDate[key]

		var h float64
		if max > 0 {
			h = float64(count) / float64(max) * chartH
		}
		x := float64(i) * (barW + gap)
		y := topPad + (chartH - h)
		bars[i] = chartBar{
			BarX:      x,
			BarY:      y,
			BarW:      barW,
			BarH:      h,
			LabelX:    x + barW/2,
			LabelY:    height - 6,
			ValueX:    x + barW/2,
			ValueY:    y - 3,
			Label:     d.Format("1/2"),
			Count:     count,
			IsToday:   key == todayKey,
			IsWeekend: d.Weekday() == time.Saturday || d.Weekday() == time.Sunday,
		}
	}
	return chartData{Width: width, Height: height, Bars: bars, Max: max}
}

func sumBars(bars []chartBar) int {
	total := 0
	for _, b := range bars {
		total += b.Count
	}
	return total
}

type cardEditData struct {
	Card           *store.ReviewCard
	EntryKind      string
	IsCloze        bool
	ClozeAnswerStr string
	Error          string
}

func (s *Server) handleCardEditGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	card, err := s.store.GetReviewCard(ctx, id)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	if card == nil {
		http.NotFound(w, r)
		return
	}
	entryKind, _ := s.store.GetEntryKind(ctx, card.ID)
	data := cardEditData{
		Card:      card,
		EntryKind: entryKind,
		IsCloze:   card.CardType == model.CardTypeCloze,
	}
	if card.ClozeAnswer != nil {
		data.ClozeAnswerStr = *card.ClozeAnswer
	}
	s.renderer.Render(w, "card_edit", data)
}

func (s *Server) handleCardEditPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	front := strings.TrimSpace(r.FormValue("front"))
	back := strings.TrimSpace(r.FormValue("back"))
	clozeAns := strings.TrimSpace(r.FormValue("cloze_answer"))
	if front == "" {
		http.Error(w, "front cannot be empty", http.StatusBadRequest)
		return
	}
	var clozePtr *string
	if clozeAns != "" {
		clozePtr = &clozeAns
	}
	if err := s.store.UpdateCard(ctx, id, front, back, clozePtr); err != nil {
		// Likely a hash collision (UNIQUE constraint). Re-render the form with an error.
		card, _ := s.store.GetReviewCard(ctx, id)
		kind, _ := s.store.GetEntryKind(ctx, id)
		data := cardEditData{
			Card:      card,
			EntryKind: kind,
			IsCloze:   card != nil && card.CardType == model.CardTypeCloze,
			Error:     "Could not save: " + err.Error(),
		}
		if data.Card != nil && data.Card.ClozeAnswer != nil {
			data.ClozeAnswerStr = *data.Card.ClozeAnswer
		}
		s.renderer.Render(w, "card_edit", data)
		return
	}
	http.Redirect(w, r, "/cards", http.StatusSeeOther)
}

func relTime(t, now time.Time, isNew bool) string {
	if isNew {
		return "new"
	}
	d := t.Sub(now)
	if d <= 0 {
		return "now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
