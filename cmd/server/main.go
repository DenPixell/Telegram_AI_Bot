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
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // база часовых поясов в образе без системной tzdata

	"expense-bot/internal/agent"
	"expense-bot/internal/config"
	"expense-bot/internal/httpapi"
	"expense-bot/internal/llm"
	"expense-bot/internal/onboarding"
	"expense-bot/internal/pipeline"
	"expense-bot/internal/service"
	"expense-bot/internal/storage/postgres"
	"expense-bot/internal/telegram"
)

const shutdownTimeout = 15 * time.Second

func main() {
	// «server healthcheck» - для healthcheck в Docker: в distroless-образе нет curl.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
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

	families := service.NewFamilies(store, service.FamiliesConfig{
		MaxMembers:    cfg.Limits.MaxFamilyMembers,
		InviteTTL:     cfg.Limits.InviteTTL,
		InviteMaxUses: cfg.Limits.InviteMaxUses,
		DefaultTZ:     cfg.AppTZ,
	})
	expenses := service.NewExpenses(store, cfg.AppTZ)
	auth := service.NewAuth(store)

	dict, err := pipeline.LoadDictionary(cfg.Synonyms.Path)
	if err != nil {
		return err
	}
	var thesaurus pipeline.Thesaurus
	if cfg.Synonyms.OnlineEnabled {
		thesaurus = pipeline.NewWiktionary(cfg.Synonyms.OnlineURL, cfg.Synonyms.OnlineTimeout)
	}
	categorizer := pipeline.NewCategorizer(dict, store, thesaurus, cfg.Synonyms.OnlineTimeout)
	analyzer := pipeline.NewAnalyzer(dict, categorizer, cfg.Limits.MaxMessageLen)
	slog.Info("dictionary loaded", "forms", dict.Size(), "online_thesaurus", cfg.Synonyms.OnlineEnabled)
	llmClient := llm.New(cfg, store)
	slog.Info("llm configured", "chain", llmClient.Name(), "agent_mode", cfg.LLM.AgentMode)
	quota := pipeline.NewQuota(store, cfg.Limits.LLMQuotaUser, cfg.Limits.LLMQuotaFamily, cfg.Limits.LLMQuotaGlobal)
	expenseAgent := agent.New(llmClient, expenses, dict, categorizer, analyzer, quota,
		agent.Config{Mode: cfg.LLM.AgentMode, MaxTokens: cfg.LLM.MaxOutputTokens})

	tgAPI := telegram.NewAPI(cfg.Telegram.APIURL, cfg.Telegram.Token)
	bot := telegram.NewBot(tgAPI, store, cfg.Telegram.BotUsername)
	botUsername := bot.Username // из BOT_USERNAME или getMe
	sender := telegram.NewSender(tgAPI, func(ctx context.Context, chatID int64) {
		// 403: пользователь заблокировал бота - больше ему не пишем.
		if err := store.SetUserActive(ctx, chatID, false); err != nil {
			slog.WarnContext(ctx, "mark user inactive failed", "err", err)
		}
	})
	bot.SetHandler(telegram.NewHandler(telegram.HandlerDeps{
		Store:       store,
		Families:    families,
		Auth:        auth,
		Agent:       expenseAgent,
		FSM:         onboarding.New(store, families, cfg.AppTZ, cfg.Limits.InviteTTL, botUsername),
		Limiter:     pipeline.NewRateLimiter(cfg.Limits.RatePerMin, time.Minute),
		Sender:      sender,
		API:         tgAPI,
		BotUsername: botUsername,
		AllowedIDs:  cfg.Telegram.AllowedIDs,
		AuthMode:    cfg.Panel.AuthMode,
		PublicURL:   cfg.Panel.PublicURL,
		InviteTTL:   cfg.Limits.InviteTTL,
		AppTZ:       cfg.AppTZ,
	}))

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

	// Бот останавливается вместе с ctx и дорабатывает начатые сообщения.
	botCtx, stopBot := context.WithCancel(ctx)
	defer stopBot()
	botDone := make(chan error, 1)
	go func() { botDone <- bot.Run(botCtx, shutdownTimeout) }()

	select {
	case <-ctx.Done():
		slog.Info("shutting down")
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case err := <-botDone:
		if err != nil {
			return err
		}
		return errors.New("telegram bot stopped unexpectedly")
	}

	stopBot()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	httpErr := srv.Shutdown(shutdownCtx)
	select {
	case err := <-botDone:
		if err != nil {
			slog.Warn("telegram bot stopped with error", "err", err)
		}
	case <-shutdownCtx.Done():
		slog.Warn("telegram bot did not stop in time")
	}
	if httpErr != nil {
		return fmt.Errorf("http shutdown: %w", httpErr)
	}
	return nil
}

// healthcheck опрашивает /healthz своего же процесса: 0 - жив, 1 - нет.
func healthcheck() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	c := http.Client{Timeout: 3 * time.Second}
	// Адрес - собственный HTTP_ADDR процесса, а не пользовательский ввод.
	resp, err := c.Get("http://" + addr + "/healthz") //nolint:gosec,noctx // G704: локальный healthcheck
	if err != nil {
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// cleanupLoop раз в час удаляет истёкшие токены входа, сессии и журнал дедупликации апдейтов.
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
			if _, err := store.DeleteOldUpdates(ctx); err != nil {
				slog.WarnContext(ctx, "cleanup processed updates failed", "err", err)
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
