package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"

	"chineseCards/internal/model"
)

// EnrichedEntry is one row in the model response's "entries" array.
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

// Result is the decoded model response.
type Result struct {
	Entries          []EnrichedEntry   `json:"entries"`
	ExampleSentences []ExampleSentence `json:"example_sentences"`
}

// promptEntry is the JSON shape we send to the model.
type promptEntry struct {
	Index   int    `json:"index"`
	Kind    string `json:"kind"`
	Chinese string `json:"chinese"`
	English string `json:"english,omitempty"`
}

// Enrich sends entries to the model and returns the structured result.
// Empty input short-circuits without any API call.
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

	req := openai.ChatCompletionRequest{
		Model: c.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: string(userJSON)},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
			JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
				Name:   "enrichment",
				Schema: enrichResponseSchema(),
				Strict: true,
			},
		},
	}

	resp, err := c.callWithRetry(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("openai enrich: %w", err)
	}
	raw := firstMessageContent(resp)
	if raw == "" {
		return nil, fmt.Errorf("empty response from openai enrich")
	}
	var result Result
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("decode enrich: %w (raw=%s)", err, truncate(raw, 500))
	}
	return &result, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ParsedAndEnrichedEntry is one row in ParseResult.Entries — the model has
// both parsed it from raw notes AND filled in translation/grammar/typo data.
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

// ParseResult is the decoded model response for ParseAndEnrich.
type ParseResult struct {
	Entries          []ParsedAndEnrichedEntry `json:"entries"`
	ExampleSentences []ParseExampleSentence   `json:"example_sentences"`
}

// ParseAndEnrich asks the model to BOTH parse free-form Chinese lesson notes
// AND enrich them in a single call. Handles complex inputs the heuristic
// parser can't: section headers, tables, parenthetical context, bare Chinese
// phrases without "=" separators. Retries on 429/5xx.
func (c *Client) ParseAndEnrich(ctx context.Context, rawText string) (*ParseResult, error) {
	if strings.TrimSpace(rawText) == "" {
		return &ParseResult{}, nil
	}
	return c.runParse(ctx, []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: parseSystemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: rawText},
	})
}

// ParseAndEnrichFile extracts text from an uploaded image using the model's
// vision input, then parses + enriches in the same call.
//
// Only image mime types are supported here — image/png, image/jpeg,
// image/webp, image/gif. PDFs return a specific error so the handler can
// prompt the user to re-upload as an image.
func (c *Client) ParseAndEnrichFile(ctx context.Context, data []byte, mimeType string) (*ParseResult, error) {
	if len(data) == 0 {
		return &ParseResult{}, nil
	}
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, fmt.Errorf("unsupported file type %q — upload an image (PNG/JPEG/WebP) instead", mimeType)
	}
	dataURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
	msg := openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleUser,
		MultiContent: []openai.ChatMessagePart{
			{
				Type: openai.ChatMessagePartTypeImageURL,
				ImageURL: &openai.ChatMessageImageURL{
					URL:    dataURL,
					Detail: openai.ImageURLDetailAuto,
				},
			},
			{
				Type: openai.ChatMessagePartTypeText,
				Text: "These are the learner's Chinese lesson notes (image). Read the content carefully and extract Chinese vocabulary items per the system instructions.",
			},
		},
	}
	return c.runParse(ctx, []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: parseSystemPrompt},
		msg,
	})
}

// runParse issues the structured-output request and decodes the reply.
// Shared by ParseAndEnrich (text) and ParseAndEnrichFile (image).
func (c *Client) runParse(ctx context.Context, msgs []openai.ChatCompletionMessage) (*ParseResult, error) {
	req := openai.ChatCompletionRequest{
		Model:    c.model,
		Messages: msgs,
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
			JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
				Name:   "parse_result",
				Schema: parseResponseSchema(),
				Strict: true,
			},
		},
	}
	resp, err := c.callWithRetry(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("openai parse: %w", err)
	}
	raw := firstMessageContent(resp)
	if raw == "" {
		return nil, fmt.Errorf("empty response from openai parse")
	}
	var result ParseResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("decode parse: %w (raw=%s)", err, truncate(raw, 500))
	}
	return &result, nil
}
