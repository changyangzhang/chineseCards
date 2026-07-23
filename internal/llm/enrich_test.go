package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"chineseCards/internal/model"
)

func TestEnrich_EmptyInputSkipsAPI(t *testing.T) {
	c := &Client{} // no SDK; .Enrich must not touch it on empty input
	res, err := c.Enrich(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(res.Entries) != 0 || len(res.ExampleSentences) != 0 {
		t.Errorf("expected empty result, got %+v", res)
	}
}

func TestEnrich_DecodesResponse(t *testing.T) {
	// What we want Enrich() to return after JSON decoding.
	enrichJSON := `{
		"entries": [
			{"source_index": 0, "pinyin": "nǐ hǎo", "english": "hello", "kind_correction": "unchanged", "suggested_cloze_word": null, "grammar_note": "the universal greeting; literally 'you good'", "typo_correction": null},
			{"source_index": 1, "pinyin": "xièxie", "english": "thank you", "kind_correction": "unchanged", "suggested_cloze_word": null, "grammar_note": null, "typo_correction": "谢谢"}
		],
		"example_sentences": [
			{"source_index": 0, "chinese": "你好，老师。", "pinyin": "nǐ hǎo, lǎoshī.", "english": "Hello, teacher.", "target_word": "你好"}
		]
	}`

	// OpenAI chat completions envelope: choices[0].message.content is a JSON
	// string containing the structured payload.
	oai, _ := json.Marshal(map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"model":   "gpt-5-mini",
		"created": 0,
		"choices": []map[string]any{
			{
				"index":         0,
				"finish_reason": "stop",
				"message": map[string]any{
					"role":    "assistant",
					"content": enrichJSON,
				},
			},
		},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, string(oai))
	}))
	defer srv.Close()

	c, err := NewClient(context.Background(), "test-key", "gpt-5-mini", Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	res, err := c.Enrich(context.Background(), []model.ParsedEntry{
		{Kind: model.KindWord, ChineseRaw: "你好", English: ""},
		{Kind: model.KindWord, ChineseRaw: "射射", English: "thank you"},
	})
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}

	if len(res.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(res.Entries))
	}
	if res.Entries[0].English != "hello" {
		t.Errorf("entry 0 english = %q", res.Entries[0].English)
	}
	if res.Entries[0].Pinyin != "nǐ hǎo" {
		t.Errorf("entry 0 pinyin = %q", res.Entries[0].Pinyin)
	}
	if res.Entries[0].GrammarNote == nil || *res.Entries[0].GrammarNote != "the universal greeting; literally 'you good'" {
		t.Errorf("entry 0 grammar_note = %v", res.Entries[0].GrammarNote)
	}
	if res.Entries[1].TypoCorrection == nil || *res.Entries[1].TypoCorrection != "谢谢" {
		t.Errorf("entry 1 typo_correction = %v", res.Entries[1].TypoCorrection)
	}
	if len(res.ExampleSentences) != 1 || res.ExampleSentences[0].TargetWord != "你好" {
		t.Errorf("example_sentences = %+v", res.ExampleSentences)
	}
	if res.ExampleSentences[0].Pinyin != "nǐ hǎo, lǎoshī." {
		t.Errorf("example pinyin = %q", res.ExampleSentences[0].Pinyin)
	}
}

func TestEnrich_MissingKey(t *testing.T) {
	_, err := NewClient(context.Background(), "", "gpt-5-mini", Options{})
	if err == nil {
		t.Errorf("expected error for empty API key")
	}
}
