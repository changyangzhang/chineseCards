package web

import (
	"math/rand/v2"
	"strings"

	"chineseCards/internal/parser"
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

func stripTrailingPuncts(s string) string {
	return strings.TrimRight(s, ".,!?;:\"。，！？；：")
}

func clozeBlankFor(token string) string {
	body := stripTrailingPuncts(token)
	trail := token[len(body):]
	return "____" + trail
}
