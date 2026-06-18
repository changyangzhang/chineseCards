package parser

import (
	"testing"
	"time"

	"chineseCards/internal/model"
)

const sampleInput = `1/6/26

实用 = convenient, practical
如果我卡住 = if I get stuck
卡住 = to get stuck
避免 = to avoid
断开 = disconnected, detached
最近几年 = the last years
正宗 = authentic
食材 = ingredients
举重 = weightlifting
我在练举重
我在做力量训练 = I do strength training`

func TestParseNotes_Sample(t *testing.T) {
	r := ParseNotes(sampleInput)

	if r.NoteDate == nil {
		t.Fatalf("expected a note date, got nil")
	}
	want := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if !r.NoteDate.Equal(want) {
		t.Errorf("date mismatch: got %v, want %v", r.NoteDate, want)
	}

	if len(r.Entries) != 11 {
		t.Fatalf("expected 11 entries, got %d", len(r.Entries))
	}

	expected := []struct {
		Kind    model.Kind
		Chinese string
		English string
	}{
		{model.KindWord, "实用", "convenient, practical"},
		{model.KindSentence, "如果我卡住", "if I get stuck"},
		{model.KindWord, "卡住", "to get stuck"},
		{model.KindWord, "避免", "to avoid"},
		{model.KindWord, "断开", "disconnected, detached"},
		{model.KindPhrase, "最近几年", "the last years"},
		{model.KindWord, "正宗", "authentic"},
		{model.KindWord, "食材", "ingredients"},
		{model.KindWord, "举重", "weightlifting"},
		{model.KindSentenceUntranslated, "我在练举重", ""},
		{model.KindSentence, "我在做力量训练", "I do strength training"},
	}

	for i, want := range expected {
		got := r.Entries[i]
		if got.Kind != want.Kind {
			t.Errorf("entry %d: kind = %q, want %q (chinese=%q)", i, got.Kind, want.Kind, got.Chinese)
		}
		if got.Chinese != want.Chinese {
			t.Errorf("entry %d: chinese = %q, want %q", i, got.Chinese, want.Chinese)
		}
		if got.English != want.English {
			t.Errorf("entry %d: english = %q, want %q", i, got.English, want.English)
		}
	}
}

func TestParseNotes_CanonicalPreservesHanzi(t *testing.T) {
	r := ParseNotes("跑步 = to run")
	if len(r.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(r.Entries))
	}
	e := r.Entries[0]
	// 2 hanzi with no pronoun/verb markers → word
	if e.Kind != model.KindWord {
		t.Errorf("kind = %q, want word", e.Kind)
	}
	if e.Chinese != "跑步" {
		t.Errorf("chinese = %q, want %q", e.Chinese, "跑步")
	}
	if e.ChineseRaw != "跑步" {
		t.Errorf("chineseRaw = %q, want %q", e.ChineseRaw, "跑步")
	}
}

func TestParseNotes_DMYDateInterpretation(t *testing.T) {
	r := ParseNotes("1/6/26\n你好 = hello")
	if r.NoteDate == nil {
		t.Fatalf("no date parsed")
	}
	if r.NoteDate.Month() != time.June || r.NoteDate.Day() != 1 {
		t.Errorf("date = %v, want 1 June (DMY)", r.NoteDate)
	}
}

func TestParseNotes_IgnoresBlankLines(t *testing.T) {
	r := ParseNotes("\n\n\n你好 = hello\n\n")
	if len(r.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(r.Entries))
	}
}

func TestParseNotes_TrailingPunctuationInHashing(t *testing.T) {
	r := ParseNotes("你好！ = hello")
	if len(r.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(r.Entries))
	}
	if r.Entries[0].Chinese != "你好" {
		t.Errorf("canonical = %q, want %q", r.Entries[0].Chinese, "你好")
	}
}

func TestClassifyKind_PronounMakesSentence(t *testing.T) {
	// Whitespace-tokenised hanzi with a pronoun → sentence
	if k := classifyKind("我 练习 中文"); k != model.KindSentence {
		t.Errorf("kind = %q, want sentence", k)
	}
	// Unsegmented hanzi containing 我 → sentence
	if k := classifyKind("我喜欢吃饭"); k != model.KindSentence {
		t.Errorf("kind = %q, want sentence", k)
	}
	// 4-hanzi chunk without any pronoun/verb marker → phrase
	if k := classifyKind("最近几年"); k != model.KindPhrase {
		t.Errorf("kind = %q, want phrase", k)
	}
}
