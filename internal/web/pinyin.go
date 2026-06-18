package web

import (
	"strings"

	gopinyin "github.com/mozillazg/go-pinyin"
)

// pinyinArgs is the conversion configuration: Tone-style (marks above
// vowels), heteronym off (one reading per hanzi — A1 learners don't need
// the alternates), fallback that returns non-CJK characters unchanged so
// punctuation and cloze blanks pass through.
var pinyinArgs = func() gopinyin.Args {
	a := gopinyin.NewArgs()
	a.Style = gopinyin.Tone
	a.Heteronym = false
	a.Fallback = func(r rune, _ gopinyin.Args) []string {
		return []string{string(r)}
	}
	return a
}()

// pinyinFor returns space-separated toned pinyin for `text`. Non-hanzi
// runes (latin letters, digits, cloze blanks, punctuation) pass through
// unchanged. Returns "" if the input has no convertible characters.
//
// Examples:
//   pinyinFor("你好")        → "nǐ hǎo"
//   pinyinFor("我在练举重")   → "wǒ zài liàn jǔ zhòng"
//   pinyinFor("我 ____ 中文") → "wǒ ____ zhōng wén"
//   pinyinFor("hello")       → "" (nothing to convert)
func pinyinFor(text string) string {
	if text == "" {
		return ""
	}
	rows := gopinyin.Pinyin(text, pinyinArgs)
	if len(rows) == 0 {
		return ""
	}
	tokens := make([]string, 0, len(rows))
	anyHanzi := false
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		token := row[0]
		// If the library converted a hanzi to its pinyin form, the token
		// differs from the original character. The fallback echoes non-CJK
		// runes verbatim — useful filler but not "pinyin".
		if !anyHanzi && !isSingleRuneNonHanzi(token) {
			anyHanzi = true
		}
		tokens = append(tokens, token)
	}
	if !anyHanzi {
		return ""
	}
	return strings.Join(tokens, " ")
}

func isSingleRuneNonHanzi(s string) bool {
	if len(s) == 0 || len(s) > 4 {
		return false
	}
	r := []rune(s)
	if len(r) != 1 {
		return false
	}
	return r[0] < 0x4E00 || r[0] > 0x9FFF
}

// pinyinOr returns `existing` when it's non-empty, otherwise computes
// pinyin for `text` on the fly. Used to surface pinyin even on entries
// that pre-date LLM enrichment (no Gemini key when imported).
func pinyinOr(existing, text string) string {
	if existing != "" {
		return existing
	}
	return pinyinFor(text)
}
