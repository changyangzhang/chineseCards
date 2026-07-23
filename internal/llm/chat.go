package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// ChatMessage is one turn in a chat conversation. Role is "user" or
// "assistant" (matching the OpenAI convention).
type ChatMessage struct {
	Role    string
	Content string
}

const chatSystemPrompt = `You are a friendly, patient Mandarin Chinese tutor talking with an absolute beginner (A1 / HSK 1). They are learning to read, write, and speak Modern Standard Mandarin with simplified characters.

How to answer every message:
- Be warm, encouraging, and concise. Short paragraphs, not lectures.
- Whenever you mention a Chinese word or sentence, always show it in this order: hanzi (pinyin) — English.
  Example: 你好 (nǐ hǎo) — hello.
- Use simplified characters (简体字). Use toned pinyin with tone marks (nǐ hǎo, not ni3 hao3).
- Prefer 1-2 small example sentences over abstract grammar rules. Show structure by example.
- Compare with the user's English instincts when it helps ("this is like how English uses…").
- If the user makes a mistake or asks something confused, gently correct without making them feel bad.
- It's fine to use a little emoji to keep the tone warm, sparingly (🙂, 👍).

What NOT to do:
- Don't dump traditional characters or unsimplified forms unless the user asks.
- Don't lecture about etymology or radicals unless asked.
- Don't switch to translating long passages — this is for questions about learning, not a translation service.

Stay in scope: questions about Chinese vocabulary, grammar, pronunciation, characters, learning strategy, or culture as it relates to language. If asked something far off-topic, briefly redirect back to Chinese learning.`

// Chat runs a multi-turn conversation against OpenAI using the tutor system
// prompt. `history` is the full conversation in chronological order; the
// LAST message must be a user message (the question we want answered).
// Returns the assistant's reply text.
func (c *Client) Chat(ctx context.Context, history []ChatMessage) (string, error) {
	if len(history) == 0 {
		return "", fmt.Errorf("chat: empty history")
	}
	if history[len(history)-1].Role != "user" {
		return "", fmt.Errorf("chat: last message must be from user")
	}

	msgs := make([]openai.ChatCompletionMessage, 0, len(history)+1)
	msgs = append(msgs, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleSystem,
		Content: chatSystemPrompt,
	})
	for _, m := range history {
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		role := openai.ChatMessageRoleUser
		if m.Role == "assistant" {
			role = openai.ChatMessageRoleAssistant
		}
		msgs = append(msgs, openai.ChatCompletionMessage{Role: role, Content: text})
	}

	req := openai.ChatCompletionRequest{
		Model:    c.model,
		Messages: msgs,
	}

	resp, err := c.callWithRetry(ctx, req)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(firstMessageContent(resp))
	if out == "" {
		return "", fmt.Errorf("empty response from openai chat")
	}
	return out, nil
}

const wordExamplePrompt = `Give a short example sentence for the Mandarin Chinese word %q. The learner is a complete beginner (A1 / HSK 1) — keep grammar and vocabulary simple.

Reply with EXACTLY three lines, no preamble, no numbering, no code fences:
Line 1: the Chinese sentence, 4-8 simplified hanzi
Line 2: toned pinyin with tone marks (e.g. "nǐ hǎo")
Line 3: an English translation

Do not use present-perfect, complex aspect particles, or complements. Present tense subject + verb + object works best.`

// WordExample generates a beginner-friendly example sentence for a single
// Chinese word. Returns (chinese, pinyin, english). Used by the daily
// Word-of-the-Day flow on the home page.
func (c *Client) WordExample(ctx context.Context, chinese string) (chineseOut, pinyin, english string, err error) {
	chinese = strings.TrimSpace(chinese)
	if chinese == "" {
		return "", "", "", fmt.Errorf("word example: empty input")
	}
	req := openai.ChatCompletionRequest{
		Model: c.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: fmt.Sprintf(wordExamplePrompt, chinese)},
		},
	}
	resp, err := c.callWithRetry(ctx, req)
	if err != nil {
		return "", "", "", err
	}

	raw := strings.TrimSpace(firstMessageContent(resp))
	lines := parseThreeLineExample(raw)
	if len(lines) < 3 {
		return "", "", "", fmt.Errorf("word example: got %d non-empty lines, want 3 (raw=%q)", len(lines), truncate(raw, 200))
	}
	return lines[0], lines[1], lines[2], nil
}

// parseThreeLineExample cleans model output: strips markdown bullets,
// numbering, code fences, and blank lines. Returns up to N cleaned lines.
func parseThreeLineExample(raw string) []string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	out := make([]string, 0, 3)
	for _, l := range strings.Split(raw, "\n") {
		l = strings.TrimSpace(l)
		l = strings.TrimPrefix(l, "- ")
		l = strings.TrimPrefix(l, "* ")
		for _, p := range []string{"1. ", "2. ", "3. ", "1) ", "2) ", "3) "} {
			l = strings.TrimPrefix(l, p)
		}
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "```") {
			continue
		}
		out = append(out, l)
	}
	return out
}

// callWithRetry wraps c.sdk.CreateChatCompletion with the same
// retry-on-transient-error policy the parse path always had. Retries
// up to 3 times with 2s / 5s backoff on 429, 500, 502, 503, 504.
func (c *Client) callWithRetry(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	var resp openai.ChatCompletionResponse
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = c.sdk.CreateChatCompletion(ctx, req)
		if err == nil {
			return resp, nil
		}
		if !isTransientOpenAIError(err) {
			return resp, fmt.Errorf("openai chat: %w", err)
		}
		select {
		case <-ctx.Done():
			return resp, ctx.Err()
		case <-time.After(time.Duration(2+3*attempt) * time.Second):
		}
	}
	return resp, fmt.Errorf("openai chat (after retries): %w", err)
}

// isTransientOpenAIError reports whether the error is worth retrying —
// 429 rate-limited or a 5xx from OpenAI. Anything else (auth failure,
// invalid model, bad request) short-circuits so we don't waste attempts.
func isTransientOpenAIError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.HTTPStatusCode {
		case 408, 409, 425, 429, 500, 502, 503, 504:
			return true
		}
	}
	s := err.Error()
	return strings.Contains(s, "timeout") || strings.Contains(s, "temporarily unavailable")
}

// firstMessageContent returns the string content of the first choice, or ""
// if the response has no choices.
func firstMessageContent(resp openai.ChatCompletionResponse) string {
	if len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Message.Content
}
