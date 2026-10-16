package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"pillgaze/internal/ai"
	"pillgaze/internal/analyze"
	"pillgaze/internal/catalog"
	"pillgaze/internal/config"
	"pillgaze/internal/fixtures"
	"pillgaze/internal/httpx"
	"pillgaze/internal/interactions"
	"pillgaze/internal/regimens"
	"pillgaze/internal/report"
	"pillgaze/internal/share"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok"))
	})

	cat, err := catalog.Load()
	if err != nil {
		log.Error("catalog data", "err", err)
		os.Exit(1)
	}
	mux.Handle("GET /api/catalog", httpx.Chain(&catalog.SearchHandler{Catalog: cat},
		httpx.NewRateLimiter(120, time.Minute).Middleware(),
	))

	analyzer := &analyze.Handler{Model: cfg.AIModelA, Mock: cfg.MockAI, Catalog: cat}
	if !cfg.MockAI {
		client := ai.New(cfg.AIBaseURL, cfg.AIAPIKey)
		// Full resolution: handwriting misreads drop and get flagged (docs/notes.md).
		client.Detail = "high"
		analyzer.AI = client
	}
	mux.Handle("POST /api/analyze", httpx.Chain(analyzer,
		httpx.NewRateLimiter(30, time.Minute).Middleware(),
		httpx.BodyLimit(analyze.MaxBodyBytes),
	))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store := share.NewStore(cfg.PinTTL)
	go store.RunJanitor(ctx, 15*time.Second)
	sharer := &share.Handler{
		Store:   store,
		Limiter: share.NewFailureLimiter(),
		Mock:    cfg.MockAI,
		Fixture: fixtures.Shared,
	}
	mux.Handle("POST /api/share", httpx.Chain(http.HandlerFunc(sharer.Share),
		httpx.NewRateLimiter(10, time.Minute).Middleware(),
		httpx.BodyLimit(share.MaxBodyBytes),
	))
	mux.Handle("POST /api/share/claim", httpx.Chain(http.HandlerFunc(sharer.Claim),
		httpx.NewRateLimiter(30, time.Minute).Middleware(),
		httpx.BodyLimit(1<<10),
	))

	idb, err := interactions.Load()
	if err != nil {
		log.Error("interactions data", "err", err)
		os.Exit(1)
	}
	mux.Handle("POST /api/interactions", httpx.Chain(&interactions.Handler{DB: idb},
		httpx.NewRateLimiter(30, time.Minute).Middleware(),
		httpx.BodyLimit(interactions.MaxBodyBytes),
	))

	regs, err := regimens.Load()
	if err != nil {
		log.Error("regimens data", "err", err)
		os.Exit(1)
	}
	mux.Handle("POST /api/regimens/resolve", httpx.Chain(&regimens.Handler{Table: regs},
		httpx.NewRateLimiter(60, time.Minute).Middleware(),
		httpx.BodyLimit(regimens.MaxBodyBytes),
	))
	mux.Handle("POST /api/report/pdf", httpx.Chain(&report.Handler{DB: idb, Table: regs},
		httpx.NewRateLimiter(20, time.Minute).Middleware(),
		httpx.BodyLimit(report.MaxBodyBytes),
	))

	if cfg.AppAPIKey == "" {
		log.Warn("APP_API_KEY empty: /api/* is unauthenticated (mock mode only)")
	}
	handler := httpx.Chain(mux,
		httpx.Recover(log),
		httpx.Logger(log),
		httpx.CORS(cfg.AllowedOrigin),
		httpx.APIKey(cfg.AppAPIKey),
	)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		// Analyze waits for the model (60 s timeout, one retry).
		WriteTimeout: 150 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info("listening", "port", cfg.Port, "mock_ai", cfg.MockAI, "pin_ttl", cfg.PinTTL.String())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "err", err)
	}
}
