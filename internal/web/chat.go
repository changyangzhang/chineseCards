package web

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"chineseCards/internal/llm"
	"chineseCards/internal/store"
)

// chatPageData is what /chat renders.
type chatPageData struct {
	Messages    []store.ChatMessage
	Pending     string // the user's just-typed message, echoed back if LLM is down
	LLMEnabled  bool
	ErrorText   string // surfaced under the input on failure
}

// chatPartialData is what the HTMX swap renders — just the message thread
// section, no full layout.
type chatPartialData struct {
	Messages  []store.ChatMessage
	ErrorText string
}

func (s *Server) handleChatGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	messages, err := s.store.ListChatMessages(ctx)
	if err != nil {
		slog.Error("list chat messages", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	s.renderer.Render(w, "chat", chatPageData{
		Messages:   messages,
		LLMEnabled: s.llm != nil,
	})
}

func (s *Server) handleChatPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	userText := strings.TrimSpace(r.FormValue("message"))
	if userText == "" {
		// Empty submit — re-render the thread without changes.
		messages, _ := s.store.ListChatMessages(ctx)
		s.renderer.RenderPartial(w, "chat", "chat-thread", chatPartialData{Messages: messages})
		return
	}

	if s.llm == nil {
		messages, _ := s.store.ListChatMessages(ctx)
		s.renderer.RenderPartial(w, "chat", "chat-thread", chatPartialData{
			Messages:  messages,
			ErrorText: "Chat needs OPENAI_API_KEY set in .env to run.",
		})
		return
	}

	// 1. Persist the user message first so the page reflects it even if the
	//    LLM call fails midway.
	if _, err := s.store.InsertChatMessage(ctx, "user", userText); err != nil {
		slog.Error("insert user chat message", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}

	// 2. Build full history (now including the just-inserted user turn) and
	//    ask the model.
	history, err := s.store.ListChatMessages(ctx)
	if err != nil {
		slog.Error("list chat for llm", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	llmHistory := chatHistoryForLLM(history)

	chatCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	reply, err := s.llm.Chat(chatCtx, llmHistory)
	if err != nil {
		slog.Warn("chat llm error", "err", err)
		s.renderer.RenderPartial(w, "chat", "chat-thread", chatPartialData{
			Messages:  history,
			ErrorText: "The tutor is taking a moment — try again in a few seconds.",
		})
		return
	}

	// 3. Persist the assistant reply.
	if _, err := s.store.InsertChatMessage(ctx, "assistant", reply); err != nil {
		slog.Error("insert assistant chat message", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}

	// 4. Re-fetch and render the swapped partial.
	all, _ := s.store.ListChatMessages(ctx)
	s.renderer.RenderPartial(w, "chat", "chat-thread", chatPartialData{Messages: all})
}

func (s *Server) handleChatClear(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.store.ClearChatMessages(ctx); err != nil {
		slog.Error("clear chat messages", "err", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/chat", http.StatusSeeOther)
}

// chatHistoryForLLM converts the DB shape into the llm-package shape.
func chatHistoryForLLM(messages []store.ChatMessage) []llm.ChatMessage {
	out := make([]llm.ChatMessage, 0, len(messages))
	for _, m := range messages {
		out = append(out, llm.ChatMessage{Role: m.Role, Content: m.Content})
	}
	return out
}
