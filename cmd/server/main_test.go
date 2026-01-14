package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewMux_HealthEndpoint(t *testing.T) {
	mux := newMux(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"GET /", http.MethodGet, "/", http.StatusOK},
		{"HEAD /", http.MethodHead, "/", http.StatusOK},
		{"POST /", http.MethodPost, "/", http.StatusMethodNotAllowed},
		{"GET /notfound", http.MethodGet, "/notfound", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, http.NoBody)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestNewMux_WebhookEndpoint(t *testing.T) {
	called := false
	wh := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mux := newMux(wh)

	req := httptest.NewRequest(http.MethodPost, "/webhook", http.NoBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if !called {
		t.Error("webhook handler was not called")
	}
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}
}

func TestNewMux_WebhookNotFound(t *testing.T) {
	mux := newMux(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodPost, "/webhook/extra", http.NoBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("got status %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestNewMailer_Mock(t *testing.T) {
	t.Setenv("BREVO_API_KEY", "")
	logger := slog.New(slog.DiscardHandler)

	mailer := newMailer(logger)
	if mailer == nil {
		t.Error("newMailer should return a mock provider")
	}
}

func TestNewMailer_Brevo(t *testing.T) {
	t.Setenv("BREVO_API_KEY", "test-key")
	t.Setenv("MAIL_FROM", "")
	t.Setenv("MAIL_NAME", "")
	logger := slog.New(slog.DiscardHandler)

	mailer := newMailer(logger)
	if mailer == nil {
		t.Error("newMailer should return a Brevo provider")
	}
}

func TestNewMailer_BrevoCustom(t *testing.T) {
	t.Setenv("BREVO_API_KEY", "test-key")
	t.Setenv("MAIL_FROM", "custom@test.com")
	t.Setenv("MAIL_NAME", "Custom Name")
	logger := slog.New(slog.DiscardHandler)

	mailer := newMailer(logger)
	if mailer == nil {
		t.Error("newMailer should return a Brevo provider")
	}
}
