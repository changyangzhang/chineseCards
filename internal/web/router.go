package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Health check — always 200, no auth (used by Fly checks).
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Login/logout live OUTSIDE the auth group so the user can actually
	// reach them while unauthenticated. The CSS stylesheet is also exempt
	// so the login page can style itself without an auth challenge.
	r.Get("/login", s.handleLoginGet)
	r.Post("/login", s.handleLoginPost)
	r.Post("/logout", s.handleLogout)
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(StaticFS()))))

	// Everything below this point requires auth when BASIC_USER/BASIC_PASS
	// are set (no-op otherwise, so local dev is unchanged). Accepts either
	// a signed session cookie (form login) or HTTP Basic header (curl).
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware(s.cfg.BasicUser, s.cfg.BasicPass))

		r.Get("/", s.handleHome)
		r.Get("/import", s.handleImportGet)
		r.Post("/import", s.handleImportPost)
		r.Get("/cards", s.handleCardsList)

		r.Get("/review", s.handleReview)
		r.Post("/review/{id}", s.handleReviewPost)
		r.Post("/review/{id}/delete", s.handleReviewDelete)

		r.Get("/stats", s.handleStats)

		r.Get("/settings", s.handleSettingsGet)
		r.Post("/settings", s.handleSettingsPost)
		r.Post("/settings/extend", s.handleSettingsExtend)

		r.Get("/cards/{id}/edit", s.handleCardEditGet)
		r.Post("/cards/{id}/edit", s.handleCardEditPost)
		r.Post("/cards/{id}/delete", s.handleCardDelete)
		r.Post("/cards/bulk-delete", s.handleCardsBulkDelete)

		r.Post("/entries/{id}/typo-accept", s.handleTypoAccept)
		r.Post("/entries/{id}/typo-dismiss", s.handleTypoDismiss)

		r.Get("/chat", s.handleChatGet)
		r.Post("/chat", s.handleChatPost)
		r.Post("/chat/clear", s.handleChatClear)

		r.Post("/admin/enrich-pending", s.handleAdminEnrichPending)
	})

	return r
}
