package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/codeGROOVE-dev/retry"
)

// BrevoProvider sends emails via Brevo (formerly Sendinblue) API.
type BrevoProvider struct {
	client   *http.Client
	logger   *slog.Logger
	apiKey   string
	fromAddr string
	fromName string
	baseURL  string // For testing; defaults to Brevo API
}

const brevoAPIURL = "https://api.brevo.com/v3/smtp/email"

// NewBrevoProvider creates a new Brevo email provider.
func NewBrevoProvider(apiKey, fromAddr, fromName string, logger *slog.Logger) *BrevoProvider {
	return &BrevoProvider{
		apiKey:   apiKey,
		fromAddr: fromAddr,
		fromName: fromName,
		client:   &http.Client{Timeout: 30 * time.Second},
		logger:   logger,
		baseURL:  brevoAPIURL,
	}
}

// brevoSendRequest represents the Brevo API send email request.
type brevoSendRequest struct {
	Sender  brevoContact   `json:"sender"`
	HTML    string         `json:"htmlContent"`
	Subject string         `json:"subject"`
	To      []brevoContact `json:"to"`
}

type brevoContact struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// Send sends an email via Brevo API.
func (b *BrevoProvider) Send(ctx context.Context, to, subject, body string) error {
	msg := brevoSendRequest{
		Sender: brevoContact{
			Email: b.fromAddr,
			Name:  b.fromName,
		},
		To: []brevoContact{
			{Email: to},
		},
		Subject: subject,
		HTML:    body,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	return retry.Do(
		func() error {
			start := time.Now()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				b.baseURL, bytes.NewReader(data))
			if err != nil {
				return fmt.Errorf("create request: %w", err)
			}

			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Api-Key", b.apiKey)

			resp, err := b.client.Do(req)
			dur := time.Since(start)

			if err != nil {
				b.logger.Warn("Brevo API request failed",
					"to", to,
					"duration_ms", dur.Milliseconds(),
					"error", err)
				return err
			}
			defer func() {
				// Drain and close body to enable connection reuse
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16)) //nolint:errcheck // best-effort drain
				_ = resp.Body.Close()                                        //nolint:errcheck // best-effort close
			}()

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				b.logger.Warn("Brevo API returned non-2xx status",
					"status", resp.StatusCode,
					"to", to,
					"will_retry", resp.StatusCode >= 500)
				err := fmt.Errorf("HTTP %d", resp.StatusCode)
				// Only retry server errors (5xx), not client errors (4xx)
				if resp.StatusCode >= 400 && resp.StatusCode < 500 {
					return retry.Unrecoverable(err)
				}
				return err
			}

			b.logger.Info("Brevo API request completed",
				"to", to,
				"duration_ms", dur.Milliseconds())

			return nil
		},
		retry.Attempts(5),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.MaxJitter(10*time.Second),
		retry.Context(ctx),
		retry.OnRetry(func(n uint, err error) {
			b.logger.Info("Retrying Brevo email send after error", "attempt", n, "error", err)
		}),
	)
}
