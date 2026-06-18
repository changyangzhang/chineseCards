package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"chineseCards/internal/cards"
	"chineseCards/internal/config"
	"chineseCards/internal/llm"
	"chineseCards/internal/store"
	"chineseCards/internal/web"
)

func Run() error {
	cfg := config.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := backfillCards(context.Background(), st); err != nil {
		slog.Error("backfill cards", "err", err)
	}
	if err := pruneCards(context.Background(), st); err != nil {
		slog.Error("prune cards", "err", err)
	}
	if n, err := st.FixLegacyClozeFronts(context.Background()); err != nil {
		slog.Error("fix legacy cloze fronts", "err", err)
	} else if n > 0 {
		slog.Info("fix legacy cloze fronts", "updated", n)
	}
	// Seed the new_per_day setting from env on first startup; subsequent
	// startups respect whatever the user changed in /settings.
	if err := st.SeedIntSettingIfAbsent(context.Background(), "new_per_day", cfg.NewPerDay); err != nil {
		slog.Warn("seed new_per_day", "err", err)
	}

	var llmClient *llm.Client
	if cfg.GeminiAPIKey != "" {
		llmClient, err = llm.NewClient(context.Background(), cfg.GeminiAPIKey, cfg.GeminiModel, llm.Options{})
		if err != nil {
			slog.Warn("LLM disabled: failed to init client", "err", err)
			llmClient = nil
		}
	}

	authEnabled := cfg.BasicUser != "" && cfg.BasicPass != ""
	renderer, err := web.NewRenderer(map[string]any{
		"auth_enabled": func() bool { return authEnabled },
	})
	if err != nil {
		return err
	}

	srv := web.NewServer(cfg, st, renderer, llmClient)

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("listening", "addr", cfg.HTTPAddr, "db", cfg.DBPath, "llm_enabled", cfg.GeminiAPIKey != "")
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpSrv.Shutdown(ctx)
}

// pruneCards enforces the "one card per entry, none for example_sentence" rule
// on the existing deck. Idempotent — re-running after the deck is already
// clean is a no-op.
func pruneCards(ctx context.Context, st *store.Store) error {
	n, err := st.PruneCards(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		slog.Info("prune cards", "deleted", n)
	}
	return nil
}

// backfillCards regenerates cards for every stored entry. Card inserts are
// hash-deduped, so this is a no-op for entries whose cards already exist; it
// adds the cards that are now possible thanks to newer generation rules
// (e.g. cloze cards added in M3, or refreshed cards once Gemini fills in
// English/cloze hints in M4).
func backfillCards(ctx context.Context, st *store.Store) error {
	entries, err := st.ListAllEntries(ctx)
	if err != nil {
		return err
	}
	var created int
	for _, e := range entries {
		existing, err := st.ListCardTypesForEntry(ctx, e.ID)
		if err != nil {
			slog.Warn("backfill list cards", "entry_id", e.ID, "err", err)
			continue
		}
		// Skip when the entry already has ANY card (1-card-per-entry rule;
		// also protects manual edits and legacy types like 'cloze').
		if len(existing) > 0 {
			continue
		}
		for _, c := range cards.Generate(e.Kind, e.ChineseRaw, e.English, e.SuggestedClozeWord) {
			res, err := st.InsertCard(ctx, e.ID, c.CardType, c.Front, c.Back, c.ClozeAnswer)
			if err != nil {
				slog.Warn("backfill insert", "entry_id", e.ID, "err", err)
				continue
			}
			if res.Inserted {
				created++
			}
		}
	}
	if created > 0 {
		slog.Info("backfill cards", "created", created)
	}
	return nil
}
