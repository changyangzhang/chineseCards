package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"google.golang.org/genai"

	"chineseCards/internal/model"
)

// EnrichedEntry is one row in the Gemini response's "entries" array.
type EnrichedEntry struct {
	SourceIndex        int     `json:"source_index"`
	Pinyin             string  `json:"pinyin"`
	English            string  `json:"english"`
	KindCorrection     string  `json:"kind_correction"`
	SuggestedClozeWord *string `json:"suggested_cloze_word"`
	GrammarNote        *string `json:"grammar_note"`
	TypoCorrection     *string `json:"typo_correction"`
}

// ExampleSentence is one row in the "example_sentences" array.
type ExampleSentence struct {
	SourceIndex int    `json:"source_index"`
	Chinese     string `json:"chinese"`
	Pinyin      string `json:"pinyin"`
	English     string `json:"english"`
	TargetWord  string `json:"target_word"`
}

// Result is the decoded Gemini response.
type Result struct {
	Entries          []EnrichedEntry   `json:"entries"`
	ExampleSentences []ExampleSentence `json:"example_sentences"`
}

// promptEntry is the JSON shape we send to Gemini.
type promptEntry struct {
	Index   int    `json:"index"`
	Kind    string `json:"kind"`
	Chinese string `json:"chinese"`
	English string `json:"english,omitempty"`
}

// Enrich sends entries to Gemini and returns the structured result. Empty input
// short-circuits without any API call.
func (c *Client) Enrich(ctx context.Context, entries []model.ParsedEntry) (*Result, error) {
	if len(entries) == 0 {
		return &Result{}, nil
	}

	payload := make([]promptEntry, len(entries))
	for i, e := range entries {
		payload[i] = promptEntry{
			Index:   i,
			Kind:    string(e.Kind),
			Chinese: e.ChineseRaw,
			English: e.English,
		}
	}
	userJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	temp := float32(0.2)
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(systemPrompt, genai.RoleUser),
		ResponseMIMEType:  "application/json",
		ResponseSchema:    responseSchema(),
		Temperature:       &temp,
		MaxOutputTokens:   8192,
	}

	resp, err := c.sdk.Models.GenerateContent(ctx, c.model,
		[]*genai.Content{genai.NewContentFromText(string(userJSON), genai.RoleUser)},
		cfg,
	)
	if err != nil {
		return nil, fmt.Errorf("gemini generate: %w", err)
	}

	raw := resp.Text()
	if raw == "" {
		return nil, fmt.Errorf("empty response from gemini")
	}

	var result Result
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("decode response: %w (raw=%s)", err, truncate(raw, 500))
	}
	return &result, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ParsedAndEnrichedEntry is one row in ParseResult.Entries — Gemini has both
// parsed it from raw notes AND filled in translation/grammar/typo data.
type ParsedAndEnrichedEntry struct {
	Chinese            string  `json:"chinese"`
	Pinyin             string  `json:"pinyin"`
	Kind               string  `json:"kind"`
	English            string  `json:"english"`
	SuggestedClozeWord *string `json:"suggested_cloze_word"`
	GrammarNote        *string `json:"grammar_note"`
	TypoCorrection     *string `json:"typo_correction"`
}

// ParseExampleSentence references its parent by Chinese text rather than an
// array index, since the input to ParseAndEnrich is raw text not an indexed
// array.
type ParseExampleSentence struct {
	ParentChinese string `json:"parent_chinese"`
	Chinese       string `json:"chinese"`
	Pinyin        string `json:"pinyin"`
	English       string `json:"english"`
	TargetWord    string `json:"target_word"`
}

// ParseResult is the decoded Gemini response for ParseAndEnrich.
type ParseResult struct {
	Entries          []ParsedAndEnrichedEntry `json:"entries"`
	ExampleSentences []ParseExampleSentence   `json:"example_sentences"`
}

// ParseAndEnrich asks Gemini to BOTH parse free-form Chinese lesson notes AND
// enrich them in a single call. Handles complex inputs that the heuristic
// parser can't: section headers, tables, parenthetical context, bare Chinese
// phrases without "=" separators. Retries on transient errors (503/429).
func (c *Client) ParseAndEnrich(ctx context.Context, rawText string) (*ParseResult, error) {
	if strings.TrimSpace(rawText) == "" {
		return &ParseResult{}, nil
	}
	return c.parseContent(ctx, []*genai.Content{
		genai.NewContentFromText(rawText, genai.RoleUser),
	})
}

// ParseAndEnrichFile extracts text from an uploaded image or PDF using Gemini's
// multimodal input, then parses + enriches in the same call. Saves the
// round-trip of a separate OCR step.
//
// Supported mime types: image/png, image/jpeg, image/webp, image/heic,
// image/heif, application/pdf. For text/plain, callers should hand the bytes
// to ParseAndEnrich(string(bytes)) instead — that's strictly cheaper.
func (c *Client) ParseAndEnrichFile(ctx context.Context, data []byte, mimeType string) (*ParseResult, error) {
	if len(data) == 0 {
		return &ParseResult{}, nil
	}
	parts := []*genai.Part{
		{InlineData: &genai.Blob{MIMEType: mimeType, Data: data}},
		{Text: "These are the learner's Chinese lesson notes (image or PDF). Read the content carefully and extract Chinese vocabulary items per the system instructions."},
	}
	return c.parseContent(ctx, []*genai.Content{{
		Role:  genai.RoleUser,
		Parts: parts,
	}})
}

// parseContent runs the parse-and-enrich call against arbitrary content
// (text-only or multimodal). Shared by ParseAndEnrich and ParseAndEnrichFile.
func (c *Client) parseContent(ctx context.Context, contents []*genai.Content) (*ParseResult, error) {
	temp := float32(0.2)
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(parseSystemPrompt, genai.RoleUser),
		ResponseMIMEType:  "application/json",
		ResponseSchema:    parseResponseSchema(),
		Temperature:       &temp,
		MaxOutputTokens:   16384,
	}

	var resp *genai.GenerateContentResponse
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = c.sdk.Models.GenerateContent(ctx, c.model, contents, cfg)
		if err == nil {
			break
		}
		if !isTransientGeminiError(err) {
			return nil, fmt.Errorf("gemini parse: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(2+3*attempt) * time.Second):
		}
	}
	if err != nil {
		return nil, fmt.Errorf("gemini parse (after retries): %w", err)
	}

	raw := resp.Text()
	if raw == "" {
		return nil, fmt.Errorf("empty response from gemini parse")
	}

	var result ParseResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("decode parse response: %w (raw=%s)", err, truncate(raw, 500))
	}
	return &result, nil
}

// isTransientGeminiError reports whether the error is one of Gemini's
// "try again later" classes — 503 UNAVAILABLE (model overloaded) or 429
// rate-limited. We retry these; we don't retry quota-exhaustion or 4xx
// classes that won't change between attempts.
func isTransientGeminiError(err error) bool {
	s := err.Error()
	return strings.Contains(s, "Error 503") ||
		strings.Contains(s, "Error 429") ||
		strings.Contains(s, "UNAVAILABLE")
}
