package cards

import (
	"testing"

	"chineseCards/internal/model"
)

func TestGenerate_WordOneCard(t *testing.T) {
	cs := Generate(model.KindWord, "实用", "convenient, practical", nil)
	if len(cs) != 1 {
		t.Fatalf("got %d cards, want 1", len(cs))
	}
	if cs[0].Front != "实用" || cs[0].Back != "convenient, practical" {
		t.Errorf("unexpected card: %+v", cs[0])
	}
	if cs[0].ClozeAnswer != nil {
		t.Errorf("word card should not have cloze_answer; got %v", cs[0].ClozeAnswer)
	}
}

func TestGenerate_WordWithoutEnglishNoCard(t *testing.T) {
	cs := Generate(model.KindWord, "实用", "", nil)
	if len(cs) != 0 {
		t.Errorf("word with no English should produce no card; got %+v", cs)
	}
}

func TestGenerate_SentenceOneCardWithClozeHint(t *testing.T) {
	hint := "力量训练"
	cs := Generate(model.KindSentence, "我在做力量训练", "I do strength training", &hint)
	if len(cs) != 1 {
		t.Fatalf("got %d cards, want 1", len(cs))
	}
	if cs[0].ClozeAnswer == nil || *cs[0].ClozeAnswer != "力量训练" {
		t.Errorf("cloze answer should use hint; got %v", cs[0].ClozeAnswer)
	}
	if cs[0].Back != "I do strength training" {
		t.Errorf("back wrong: %q", cs[0].Back)
	}
}

func TestGenerate_SentenceUntranslatedOneCardClozeOnly(t *testing.T) {
	hint := "举重"
	cs := Generate(model.KindSentenceUntranslated, "我在练举重", "", &hint)
	if len(cs) != 1 {
		t.Fatalf("got %d cards, want 1", len(cs))
	}
	if cs[0].Back != "" {
		t.Errorf("untranslated should have empty back; got %q", cs[0].Back)
	}
	if cs[0].ClozeAnswer == nil || *cs[0].ClozeAnswer != "举重" {
		t.Errorf("cloze answer = %v, want 举重", cs[0].ClozeAnswer)
	}
}

func TestGenerate_ExampleSentenceNoCard(t *testing.T) {
	hint := "实用"
	cs := Generate(model.KindExampleSentence, "这个工具很实用", "This tool is very practical", &hint)
	if len(cs) != 0 {
		t.Errorf("example_sentence should produce no card; got %+v", cs)
	}
}

func TestGenerate_PhraseOneCard(t *testing.T) {
	cs := Generate(model.KindPhrase, "最近几年", "the last few years", nil)
	if len(cs) != 1 || cs[0].Front != "最近几年" {
		t.Errorf("phrase should produce 1 card; got %+v", cs)
	}
}

func TestPickClozeAnswer_PrefersHint(t *testing.T) {
	hint := "举重"
	got := pickClozeAnswer("我在练举重", &hint)
	if got != "举重" {
		t.Errorf("got %q, want hint '举重'", got)
	}
}

func TestPickClozeAnswer_HintMissesFallsBackToWholeString(t *testing.T) {
	// Unsegmented hanzi sentence with a hint that's not a substring: the
	// fallback over strings.Fields sees one token (the whole sentence) and
	// returns it because it's not in the stopword set. This is best-effort —
	// real Chinese cloze relies on the LLM-supplied hint anyway.
	hint := "不在里面"
	got := pickClozeAnswer("我在练举重", &hint)
	if got == "" {
		t.Errorf("expected non-empty fallback, got empty")
	}
}

func TestPickClozeAnswer_WhitespaceTokenisedFallback(t *testing.T) {
	// Pinyin-style tokenised input: hint missing, fallback picks longest non-stopword.
	hint := "missing"
	got := pickClozeAnswer("wǒ zài liànxí jǔzhòng", &hint)
	if got == "" || got == "wǒ" {
		t.Errorf("expected fallback to a content token, got %q", got)
	}
}
