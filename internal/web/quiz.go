package web

import (
	"math/rand/v2"
	"strings"

	"chineseCards/internal/parser"
	"chineseCards/internal/store"
)

// rotateBlank picks a random non-stopword token from the sentence and returns
// the front (with that token replaced by ____) and the blanked answer.
// Returns (sentence, "") when no eligible token exists.
func rotateBlank(sentence string) (front, answer string) {
	words := strings.Fields(sentence)
	candidates := make([]int, 0, len(words))
	for i, w := range words {
		clean := stripTrailingPuncts(strings.ToLower(w))
		if clean == "" || parser.ChineseStopwords[clean] {
			continue
		}
		candidates = append(candidates, i)
	}
	if len(candidates) == 0 {
		return sentence, ""
	}
	pick := candidates[rand.IntN(len(candidates))]
	masked := make([]string, len(words))
	copy(masked, words)
	answer = stripTrailingPuncts(words[pick])
	masked[pick] = clozeBlankFor(words[pick])
	front = strings.Join(masked, " ")
	return
}

// buildChoices puts the correct answer at a random position among the
// distractors and returns the full shuffled list.
func buildChoices(correct string, distractors []string) []string {
	out := make([]string, 0, 1+len(distractors))
	out = append(out, correct)
	out = append(out, distractors...)
	rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// buildChinChoices is buildChoices for Chinese-side choices: it keeps each
// option paired with its pinyin through the shuffle. Distractors that have
// no DB-supplied pinyin get one computed via the offline library so every
// hanzi choice has reading help underneath.
func buildChinChoices(correct, correctPinyin string, distractors []store.DistractorChoice) (texts, pinyins []string) {
	n := 1 + len(distractors)
	texts = make([]string, 0, n)
	pinyins = make([]string, 0, n)
	texts = append(texts, correct)
	pinyins = append(pinyins, pinyinOr(correctPinyin, correct))
	for _, d := range distractors {
		texts = append(texts, d.Text)
		pinyins = append(pinyins, pinyinOr(d.Pinyin, d.Text))
	}
	rand.Shuffle(n, func(i, j int) {
		texts[i], texts[j] = texts[j], texts[i]
		pinyins[i], pinyins[j] = pinyins[j], pinyins[i]
	})
	return texts, pinyins
}

func stripTrailingPuncts(s string) string {
	return strings.TrimRight(s, ".,!?;:\"。，！？；：")
}

func clozeBlankFor(token string) string {
	body := stripTrailingPuncts(token)
	trail := token[len(body):]
	return "____" + trail
}
