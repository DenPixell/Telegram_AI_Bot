// Единственный бинарник: REST API панели и Telegram-бот над общим слоем service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // база часовых поясов в образе без системной tzdata

	"expense-bot/internal/config"
	"expense-bot/internal/httpapi"
	"expense-bot/internal/llm"
	"expense-bot/internal/pipeline"
	"expense-bot/internal/service"
	"expense-bot/internal/storage/postgres"
)

const shutdownTimeout = 15 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	slog.SetDefault(newLogger(cfg.LogLevel))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	connectCtx, cancel := context.WithTimeout(ctx, time.Minute)
	pool, err := postgres.Connect(connectCtx, cfg.DBDSN)
	cancel()
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := postgres.Migrate(ctx, pool); err != nil {
		return err
	}
	store := postgres.New(pool)

	dict, err := pipeline.LoadDictionary(cfg.Synonyms.Path)
	if err != nil {
		return err
	}
	var thesaurus pipeline.Thesaurus
	if cfg.Synonyms.OnlineEnabled {
		thesaurus = pipeline.NewWiktionary(cfg.Synonyms.OnlineURL, cfg.Synonyms.OnlineTimeout)
	}
	categorizer := pipeline.NewCategorizer(dict, store, thesaurus, cfg.Synonyms.OnlineTimeout)
	// TODO(этап 7-8): analyzer, LLM-клиент, лимиты и квоты используются ботом и агентом.
	_ = pipeline.NewAnalyzer(dict, categorizer, cfg.Limits.MaxMessageLen)
	slog.Info("dictionary loaded", "forms", dict.Size(), "online_thesaurus", cfg.Synonyms.OnlineEnabled)
	llmClient := llm.New(cfg, store)
	slog.Info("llm configured", "chain", llmClient.Name(), "agent_mode", cfg.LLM.AgentMode)

	families := service.NewFamilies(store, service.FamiliesConfig{
		MaxMembers:    cfg.Limits.MaxFamilyMembers,
		InviteTTL:     cfg.Limits.InviteTTL,
		InviteMaxUses: cfg.Limits.InviteMaxUses,
		DefaultTZ:     cfg.AppTZ,
	})
	expenses := service.NewExpenses(store, cfg.AppTZ)
	auth := service.NewAuth(store)
	// TODO(этап 8): username из getMe, если BOT_USERNAME не задан.
	botUsername := func() string { return cfg.Telegram.BotUsername }

	api := httpapi.New(httpapi.Deps{
		DB:            store,
		Store:         store,
		Families:      families,
		Expenses:      expenses,
		Auth:          auth,
		AuthMode:      cfg.Panel.AuthMode,
		PanelFamilyID: cfg.Panel.FamilyID,
		PublicURL:     cfg.Panel.PublicURL,
		BotUsername:   botUsername,
		ExpandTerms:   dict.Expand,
	})
	if cfg.Panel.AuthMode == config.PanelAuthNone {
		slog.Warn("panel authentication is disabled (PANEL_AUTH_MODE=none), use only for local demo")
	}
	go cleanupLoop(ctx, store)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// TODO(этап 8): запуск telegram-адаптера (long polling) в этой же горутине-группе.
	slog.Info("telegram adapter is not implemented yet", "llm_model", cfg.LLM.Model)

	select {
	case <-ctx.Done():
		slog.Info("shutting down")
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	return nil
}

// cleanupLoop раз в час удаляет истёкшие токены входа и сессии.
func cleanupLoop(ctx context.Context, store *postgres.Store) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := store.DeleteExpiredAuth(ctx); err != nil {
				slog.WarnContext(ctx, "cleanup failed", "err", err)
			} else if n > 0 {
				slog.InfoContext(ctx, "expired auth records removed", "count", n)
			}
		}
	}
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}
