package web

import (
	"strings"
	"testing"
)

// rotateBlank is called on whitespace-tokenised text (e.g. pinyin) where
// stopword detection by token works. For unsegmented hanzi the LLM-supplied
// cloze_answer is used instead and rotateBlank isn't invoked.

func TestRotateBlank_PicksNonStopword(t *testing.T) {
	// "我 喜欢 吃 饭" — pronoun (我) and aspect particle don't apply; "喜欢" and "饭" are content.
	// Use pinyin form so whitespace tokenisation works and our stopword set matches.
	front, answer := rotateBlank("wǒ xǐhuān chī fàn")
	if answer == "" {
		t.Errorf("answer must be non-empty for a sentence with content words")
	}
	lower := strings.ToLower(answer)
	if lower == "我" {
		t.Errorf("answer=%q must not be the pronoun", answer)
	}
	if !strings.Contains(front, "____") {
		t.Errorf("front=%q must contain ____", front)
	}
}

func TestRotateBlank_NeverPicksStopword(t *testing.T) {
	for i := 0; i < 200; i++ {
		_, a := rotateBlank("我 喜欢 吃 饭")
		lower := strings.ToLower(a)
		if lower == "我" || lower == "是" || lower == "的" {
			t.Fatalf("iteration %d picked stopword: %q", i, a)
		}
	}
}

func TestRotateBlank_StopwordOnlyEmpty(t *testing.T) {
	front, answer := rotateBlank("我 是 的")
	if answer != "" {
		t.Errorf("expected empty answer for stopword-only sentence, got %q", answer)
	}
	if front != "我 是 的" {
		t.Errorf("front should be unchanged when no candidates; got %q", front)
	}
}

func TestRotateBlank_PreservesTrailingPunct(t *testing.T) {
	for i := 0; i < 30; i++ {
		front, ans := rotateBlank("我 喜欢 吃 饭.")
		// answer should be a token without the trailing dot
		if strings.HasSuffix(ans, ".") {
			t.Errorf("answer should not include trailing punct: %q", ans)
		}
		// front should still end with .
		if !strings.HasSuffix(front, ".") {
			t.Errorf("trailing punct lost: %q", front)
		}
	}
}

func TestBuildChoices_AlwaysContainsCorrect(t *testing.T) {
	for i := 0; i < 30; i++ {
		out := buildChoices("right", []string{"a", "b", "c"})
		if len(out) != 4 {
			t.Fatalf("len=%d, want 4", len(out))
		}
		found := false
		for _, v := range out {
			if v == "right" {
				found = true
			}
		}
		if !found {
			t.Errorf("correct answer missing: %v", out)
		}
	}
}

func TestBuildChoices_Shuffles(t *testing.T) {
	atZero := 0
	for i := 0; i < 50; i++ {
		out := buildChoices("right", []string{"a", "b", "c"})
		if out[0] == "right" {
			atZero++
		}
	}
	if atZero == 50 || atZero == 0 {
		t.Errorf("shuffle not shuffling: atZero=%d/50", atZero)
	}
}

func TestBuildChoices_NoDistractors(t *testing.T) {
	out := buildChoices("right", nil)
	if len(out) != 1 || out[0] != "right" {
		t.Errorf("expected [right], got %v", out)
	}
}
