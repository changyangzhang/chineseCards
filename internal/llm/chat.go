package llm

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"
)

// ChatMessage is one turn in a chat conversation. Role is "user" or
// "assistant" (matching the OpenAI convention; we translate to Gemini's
// "user"/"model" inside Chat).
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

// Chat runs a multi-turn conversation against Gemini using the tutor system
// prompt. `history` is the full conversation in chronological order; the LAST
// message must be a user message (the question we want answered). Returns the
// assistant's reply text.
//
// Empty history or a history not ending in a user message returns an error.
func (c *Client) Chat(ctx context.Context, history []ChatMessage) (string, error) {
	if len(history) == 0 {
		return "", fmt.Errorf("chat: empty history")
	}
	if history[len(history)-1].Role != "user" {
		return "", fmt.Errorf("chat: last message must be from user")
	}

	contents := make([]*genai.Content, 0, len(history))
	for _, m := range history {
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		var role genai.Role = genai.RoleUser
		if m.Role == "assistant" {
			role = genai.RoleModel
		}
		contents = append(contents, genai.NewContentFromText(text, role))
	}

	temp := float32(0.6) // a touch warmer than the structured-output paths
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(chatSystemPrompt, genai.RoleUser),
		Temperature:       &temp,
		MaxOutputTokens:   1024,
	}

	resp, err := c.sdk.Models.GenerateContent(ctx, c.model, contents, cfg)
	if err != nil {
		return "", fmt.Errorf("gemini chat: %w", err)
	}
	out := strings.TrimSpace(resp.Text())
	if out == "" {
		return "", fmt.Errorf("empty response from gemini chat")
	}
	return out, nil
}
