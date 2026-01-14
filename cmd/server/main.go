// Package main implements custhook, a GitHub Marketplace webhook handler
// that sends email notifications for reviewGOOSE app events.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/codeGROOVE-dev/custhook/pkg/email"
	"github.com/codeGROOVE-dev/custhook/pkg/webhook"
)

const (
	readTimeout    = 10 * time.Second
	writeTimeout   = 10 * time.Second
	idleTimeout    = 120 * time.Second
	maxHeaderBytes = 1 << 20 // 1MB
	shutdownWait   = 5 * time.Second

	defaultFromAddr = "noreply@codeGROOVE.dev"
	defaultFromName = "reviewGOOSE"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	secret := os.Getenv("WEBHOOK_SECRET")
	if secret == "" {
		logger.Error("WEBHOOK_SECRET environment variable is required")
		os.Exit(1)
	}

	mailer := newMailer(logger)
	wh := webhook.NewHandler(secret, mailer, logger)
	mux := newMux(wh)

	server := &http.Server{
		Addr:           ":" + port,
		Handler:        mux,
		ReadTimeout:    readTimeout,
		WriteTimeout:   writeTimeout,
		IdleTimeout:    idleTimeout,
		MaxHeaderBytes: maxHeaderBytes,
	}

	done := make(chan struct{})
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt)
		<-sigint

		logger.Info("shutting down server...")

		ctx, cancel := context.WithTimeout(context.Background(), shutdownWait)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			logger.Error("server shutdown error", "error", err)
		}
		close(done)
	}()

	logger.Info("starting server", "port", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}

	<-done
	logger.Info("server stopped")
}

func newMailer(logger *slog.Logger) email.Provider {
	key := os.Getenv("BREVO_API_KEY")
	if key == "" {
		logger.Info("BREVO_API_KEY not set, using mock email provider")
		return email.NewMockProvider(logger)
	}
	from := os.Getenv("MAIL_FROM")
	if from == "" {
		from = defaultFromAddr
	}
	name := os.Getenv("MAIL_NAME")
	if name == "" {
		name = defaultFromName
	}
	logger.Info("using Brevo email provider", "from", from)
	return email.NewBrevoProvider(key, from, name, logger)
}

func newMux(wh http.Handler) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("custhook is running\n")) //nolint:errcheck // health check
	})

	mux.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/webhook" {
			http.NotFound(w, r)
			return
		}
		wh.ServeHTTP(w, r)
	})

	return mux
}
