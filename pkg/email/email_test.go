package email

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMockProvider_Send(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	mp := NewMockProvider(logger)

	err := mp.Send(context.Background(), "test@example.com", "Test Subject", "<p>Test Body</p>")
	if err != nil {
		t.Errorf("Send() error = %v, want nil", err)
	}
}

func TestBrevoProvider_NewBrevoProvider(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	bp := NewBrevoProvider("api-key", "from@test.com", "Sender Name", logger)

	if bp.apiKey != "api-key" {
		t.Errorf("apiKey = %q, want %q", bp.apiKey, "api-key")
	}
	if bp.fromAddr != "from@test.com" {
		t.Errorf("fromAddr = %q, want %q", bp.fromAddr, "from@test.com")
	}
	if bp.fromName != "Sender Name" {
		t.Errorf("fromName = %q, want %q", bp.fromName, "Sender Name")
	}
	if bp.client == nil {
		t.Error("client should not be nil")
	}
	if bp.logger == nil {
		t.Error("logger should not be nil")
	}
	if bp.baseURL != brevoAPIURL {
		t.Errorf("baseURL = %q, want %q", bp.baseURL, brevoAPIURL)
	}
}

func TestBrevoProvider_Send_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("got method %s, want POST", r.Method)
		}
		if r.Header.Get("Api-Key") != "test-api-key" {
			t.Errorf("got api-key %q, want %q", r.Header.Get("Api-Key"), "test-api-key")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("got content-type %q, want %q", r.Header.Get("Content-Type"), "application/json")
		}

		var req brevoSendRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}
		if req.Sender.Email != "from@example.com" {
			t.Errorf("sender email = %q, want %q", req.Sender.Email, "from@example.com")
		}
		if req.Subject != "Test Subject" {
			t.Errorf("subject = %q, want %q", req.Subject, "Test Subject")
		}
		if len(req.To) != 1 || req.To[0].Email != "to@example.com" {
			t.Errorf("to = %v, want [{Email: to@example.com}]", req.To)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := slog.New(slog.DiscardHandler)
	bp := NewBrevoProvider("test-api-key", "from@example.com", "Test Sender", logger)
	bp.baseURL = server.URL

	err := bp.Send(context.Background(), "to@example.com", "Test Subject", "<p>Body</p>")
	if err != nil {
		t.Errorf("Send() error = %v, want nil", err)
	}
}

func TestBrevoProvider_Send_ClientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	logger := slog.New(slog.DiscardHandler)
	bp := NewBrevoProvider("test-api-key", "from@example.com", "Test Sender", logger)
	bp.baseURL = server.URL

	err := bp.Send(context.Background(), "to@example.com", "Test Subject", "<p>Body</p>")
	if err == nil {
		t.Error("Send() should return error for 4xx status")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention HTTP 400, got: %v", err)
	}
}

func TestBrevoProvider_Send_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	logger := slog.New(slog.DiscardHandler)
	bp := NewBrevoProvider("test-api-key", "from@example.com", "Test Sender", logger)
	bp.baseURL = server.URL

	// Use short timeout to avoid waiting for all retries
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := bp.Send(ctx, "to@example.com", "Test Subject", "<p>Body</p>")
	if err == nil {
		t.Error("Send() should return error for 5xx status")
	}
}

func TestBrevoProvider_Send_ContextCancelled(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	bp := NewBrevoProvider("test-api-key", "from@example.com", "Test Sender", logger)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := bp.Send(ctx, "to@example.com", "Subject", "<p>Body</p>")
	if err == nil {
		t.Error("Send() should return error when context is cancelled")
	}
}

func TestBrevoProvider_Send_NetworkError(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	bp := NewBrevoProvider("test-api-key", "from@example.com", "Test Sender", logger)
	bp.baseURL = "http://localhost:1" // Invalid port, will fail to connect

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := bp.Send(ctx, "to@example.com", "Subject", "<p>Body</p>")
	if err == nil {
		t.Error("Send() should return error for network failure")
	}
}
