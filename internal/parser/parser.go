package parser

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"chineseCards/internal/model"
)

type Result struct {
	NoteDate *time.Time
	Entries  []model.ParsedEntry
}

// ParseNotes turns a raw paste of lesson notes into a Result.
// The function is pure and runs offline; LLM enrichment happens later.
func ParseNotes(raw string) Result {
	var out Result

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Date header? If so, set date and skip.
		if d, ok := tryParseDate(line); ok {
			d := d
			out.NoteDate = &d
			continue
		}

		entry, ok := parseEntryLine(line)
		if !ok {
			continue
		}
		out.Entries = append(out.Entries, entry)
	}

	return out
}

func parseEntryLine(line string) (model.ParsedEntry, bool) {
	// Equals-separated line: chinese = english(,english2,...)
	if idx := strings.Index(line, "="); idx >= 0 {
		lhs := strings.TrimSpace(line[:idx])
		rhs := strings.TrimSpace(line[idx+1:])
		if lhs == "" {
			return model.ParsedEntry{}, false
		}

		chineseRaw := lhs
		english := rhs

		kind := classifyKind(lhs)
		chinese := CanonicalChinese(lhs)

		return model.ParsedEntry{
			Kind:       kind,
			Chinese:    chinese,
			ChineseRaw: chineseRaw,
			English:    english,
		}, true
	}

	// Bare line, treat as untranslated sentence (or single untranslated word).
	chineseRaw := line
	chinese := CanonicalChinese(line)
	return model.ParsedEntry{
		Kind:       model.KindSentenceUntranslated,
		Chinese:    chinese,
		ChineseRaw: chineseRaw,
		English:    "",
	}, true
}

func classifyKind(lhs string) model.Kind {
	lower := strings.ToLower(lhs)

	// If the LHS is whitespace-tokenised (mixed pinyin/latin, or hanzi the user
	// chose to separate), inspect tokens for pronoun/verb markers.
	tokens := strings.Fields(lower)
	if len(tokens) > 1 {
		for _, t := range tokens {
			clean := strings.Trim(t, ".,!?;:。，！？；：")
			if chinesePronouns[clean] || chineseVerbs[clean] {
				return model.KindSentence
			}
		}
		return model.KindPhrase
	}

	// Single-token / unsegmented hanzi: use character count and a hanzi pronoun
	// scan to classify.
	hanziLen := utf8.RuneCountInString(lhs)
	if hanziLen <= 1 {
		return model.KindWord
	}
	for token := range chinesePronouns {
		if strings.Contains(lhs, token) {
			return model.KindSentence
		}
	}
	for token := range chineseVerbs {
		if strings.Contains(lhs, token) {
			return model.KindSentence
		}
	}
	if hanziLen <= 3 {
		return model.KindWord
	}
	return model.KindPhrase
}

// CanonicalChinese returns the form used for hashing/dedup.
//   - leading/trailing whitespace trimmed
//   - trailing ASCII or Chinese sentence punctuation trimmed
//   - any Latin letters lowercased (for romanised entries)
//
// We deliberately don't touch hanzi themselves — there's no case, and
// normalising simplified vs traditional would silently collapse distinct
// entries.
func CanonicalChinese(s string) string {
	out := strings.TrimSpace(s)
	out = strings.TrimRightFunc(out, func(r rune) bool {
		switch r {
		case '.', '!', '?', '。', '！', '？':
			return true
		}
		return unicode.IsSpace(r)
	})
	var b strings.Builder
	b.Grow(len(out))
	for _, r := range out {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Date parsing.
//
// Order of attempts:
//  1. DMY short ("2/1/06") — matches "1/6/26" -> 1 June 2026
//  2. ISO ("2006-01-02")
//  3. Long month-name format (e.g. "January 6, 2026")
var monthNameRe = regexp.MustCompile(`^(?i)(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d{1,2})(?:,\s*(\d{2,4}))?$`)

func tryParseDate(line string) (time.Time, bool) {
	if t, err := time.Parse("2/1/06", line); err == nil {
		return t, true
	}
	if t, err := time.Parse("2/1/2006", line); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02", line); err == nil {
		return t, true
	}
	if m := monthNameRe.FindStringSubmatch(line); m != nil {
		layout := "January 2, 2006"
		if m[3] == "" {
			layout = "January 2"
		}
		if t, err := time.Parse(layout, line); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
