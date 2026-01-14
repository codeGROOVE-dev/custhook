// Package webhook provides HTTP handlers for processing GitHub Marketplace webhook events.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/custhook/pkg/email"
)

const (
	maxPayloadSize = 1 << 20 // 1MB
	notifyEmail    = "new-registration@reviewGOOSE.dev"
)

// MarketplaceEvent represents a GitHub Marketplace webhook ev.
type MarketplaceEvent struct {
	Action              string              `json:"action"`
	EffectiveDate       string              `json:"effective_date"`
	MarketplacePurchase MarketplacePurchase `json:"marketplace_purchase"`
	Sender              Sender              `json:"sender"`
}

// MarketplacePurchase contains purchase details.
//
//nolint:govet // fieldalignment: JSON struct, readability over 8-byte savings
type MarketplacePurchase struct {
	Account      Account `json:"account"`
	Plan         Plan    `json:"plan"`
	BillingCycle string  `json:"billing_cycle"`
	UnitCount    int     `json:"unit_count"`
}

// Account represents the GitHub account making the purchase.
type Account struct {
	Login             string `json:"login"`
	Email             string `json:"email"`
	Type              string `json:"type"` // User or Organization
	OrganizationLogin string `json:"organization_billing_email,omitempty"`
}

// Plan represents the marketplace plan.
type Plan struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Bullets     []string `json:"bullets"`
}

// Sender represents the GitHub user who triggered the ev.
type Sender struct {
	Login string `json:"login"`
	ID    int    `json:"id"`
}

// Handler handles GitHub Marketplace webhook events.
type Handler struct {
	emailProvider email.Provider
	logger        *slog.Logger
	secret        string
}

// NewHandler creates a new webhook handler.
func NewHandler(secret string, emailProvider email.Provider, logger *slog.Logger) *Handler {
	return &Handler{
		secret:        secret,
		emailProvider: emailProvider,
		logger:        logger,
	}
}

// ServeHTTP processes GitHub Marketplace webhook events.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()

	id := r.Header.Get("X-GitHub-Delivery") //nolint:canonicalheader // GitHub header
	event := r.Header.Get("X-GitHub-Event") //nolint:canonicalheader // GitHub header
	sig := r.Header.Get("X-Hub-Signature-256")

	h.logger.Info("webhook request received",
		"method", r.Method,
		"remote_addr", r.RemoteAddr,
		"event", event,
		"id", id)

	if r.Method != http.MethodPost {
		h.logger.Warn("webhook rejected: invalid method",
			"method", r.Method,
			"id", id)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Handle ping events - send notification
	if event == "ping" {
		h.logger.Info("ping event received",
			"event", event,
			"id", id)
		subj := "[reviewGOOSE] Webhook ping received"
		body := formatPingEmailBody(id, r.RemoteAddr)
		if err := h.emailProvider.Send(ctx, notifyEmail, subj, body); err != nil {
			h.logger.Error("failed to send ping notification email",
				"error", err,
				"id", id)
		} else {
			h.logger.Info("ping notification email sent",
				"to", notifyEmail,
				"id", id)
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("pong")); err != nil {
			h.logger.Error("failed to write ping response", "error", err, "id", id)
		}
		h.logger.Info("ping request completed", "id", id, "duration_ms", time.Since(start).Milliseconds())
		return
	}

	// Only handle marketplace_purchase events, ignore other event types
	if event != "marketplace_purchase" {
		h.logger.Info("ignoring non-marketplace event",
			"event", event,
			"id", id)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Check content length before reading
	if r.ContentLength > maxPayloadSize {
		h.logger.Warn("webhook rejected: payload too large",
			"content_length", r.ContentLength,
			"id", id)
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}

	// Read body
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPayloadSize))
	_ = r.Body.Close() //nolint:errcheck // best-effort close after read
	if err != nil {
		h.logger.Error("error reading webhook body", "error", err, "id", id)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Verify signature
	if !verifySignature(body, sig, h.secret) {
		h.logger.Warn("webhook rejected: signature verification failed",
			"id", id,
			"has_sig", sig != "",
			"has_secret", h.secret != "")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse payload
	var ev MarketplaceEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		h.logger.Error("error parsing webhook payload", "error", err, "id", id)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	h.logger.Info("marketplace event received",
		"action", ev.Action,
		"account", ev.MarketplacePurchase.Account.Login,
		"account_type", ev.MarketplacePurchase.Account.Type,
		"plan", ev.MarketplacePurchase.Plan.Name,
		"sender", ev.Sender.Login,
		"id", id)

	// Send notification email
	subj := fmt.Sprintf("[reviewGOOSE] %s: %s", ev.Action, ev.MarketplacePurchase.Account.Login)

	if err := h.emailProvider.Send(ctx, notifyEmail, subj, formatEmailBody(&ev)); err != nil {
		h.logger.Error("failed to send notification email", "error", err, "id", id)
		// Still return 200 to GitHub so it doesn't retry
	} else {
		h.logger.Info("notification email sent", "to", notifyEmail, "subject", subj, "id", id)
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		h.logger.Error("failed to write response", "error", err, "id", id)
	}

	h.logger.Info("webhook request completed", "id", id, "duration_ms", time.Since(start).Milliseconds())
}

// verifySignature validates the GitHub webhook signature.
// Uses constant-time operations to prevent timing attacks.
func verifySignature(payload []byte, signature, secret string) bool {
	if secret == "" {
		return false
	}

	// Always compute HMAC to maintain constant time regardless of input
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	// Length check prevents timing attacks from hmac.Equal on mismatched lengths
	if len(signature) != len(expected) {
		return false
	}

	return hmac.Equal([]byte(signature), []byte(expected))
}

// formatEmailBody creates an HTML email body for the marketplace event.
func formatEmailBody(ev *MarketplaceEvent) string {
	typ := ev.MarketplacePurchase.Account.Type
	if typ == "" {
		typ = "Unknown"
	}

	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html>
<html>
<head>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; line-height: 1.6; color: #333; }
.container { max-width: 600px; margin: 0 auto; padding: 20px; }
.header { background: #0366d6; color: white; padding: 20px; border-radius: 8px 8px 0 0; }
.content { background: #f6f8fa; padding: 20px; border-radius: 0 0 8px 8px; }
.field { margin-bottom: 12px; }
.label { font-weight: 600; color: #586069; }
.value { color: #24292e; }
.action-purchased { color: #28a745; }
.action-cancelled { color: #d73a49; }
.action-changed { color: #f9826c; }
.action-pending_change { color: #6f42c1; }
.action-pending_change_cancelled { color: #959da5; }
</style>
</head>
<body>
<div class="container">
<div class="header">
<h1 style="margin: 0;">reviewGOOSE Marketplace Event</h1>
</div>
<div class="content">
`)

	field := func(label, value string) {
		sb.WriteString(`<div class="field"><span class="label">`)
		sb.WriteString(label)
		sb.WriteString(`:</span> <span class="value">`)
		sb.WriteString(html.EscapeString(value))
		sb.WriteString(`</span></div>`)
	}

	// Action with color class
	sb.WriteString(`<div class="field"><span class="label">Action:</span> <span class="value action-`)
	sb.WriteString(html.EscapeString(ev.Action))
	sb.WriteString(`">`)
	sb.WriteString(html.EscapeString(ev.Action))
	sb.WriteString(`</span></div>`)

	acct := ev.MarketplacePurchase.Account
	field("Account", acct.Login+" ("+typ+")")
	if acct.Email != "" {
		field("Account Email", acct.Email)
	}

	field("Plan", ev.MarketplacePurchase.Plan.Name)
	if ev.MarketplacePurchase.BillingCycle != "" {
		field("Billing Cycle", ev.MarketplacePurchase.BillingCycle)
	}
	if ev.MarketplacePurchase.UnitCount > 0 {
		field("Units", strconv.Itoa(ev.MarketplacePurchase.UnitCount))
	}

	field("Initiated by", ev.Sender.Login)
	if ev.EffectiveDate != "" {
		field("Effective Date", ev.EffectiveDate)
	}

	sb.WriteString(`</div>
</div>
</body>
</html>`)

	return sb.String()
}

// formatPingEmailBody creates an HTML email body for ping events.
func formatPingEmailBody(deliveryID, remoteAddr string) string {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html>
<html>
<head>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; line-height: 1.6; color: #333; }
.container { max-width: 600px; margin: 0 auto; padding: 20px; }
.header { background: #28a745; color: white; padding: 20px; border-radius: 8px 8px 0 0; }
.content { background: #f6f8fa; padding: 20px; border-radius: 0 0 8px 8px; }
.field { margin-bottom: 12px; }
.label { font-weight: 600; color: #586069; }
.value { color: #24292e; }
</style>
</head>
<body>
<div class="container">
<div class="header">
<h1 style="margin: 0;">reviewGOOSE Webhook Ping</h1>
</div>
<div class="content">
<p>GitHub has successfully pinged the reviewGOOSE webhook endpoint.</p>
`)

	sb.WriteString(`<div class="field"><span class="label">Delivery ID:</span> <span class="value">`)
	sb.WriteString(html.EscapeString(deliveryID))
	sb.WriteString(`</span></div>`)

	sb.WriteString(`<div class="field"><span class="label">Remote Address:</span> <span class="value">`)
	sb.WriteString(html.EscapeString(remoteAddr))
	sb.WriteString(`</span></div>`)

	sb.WriteString(`<div class="field"><span class="label">Timestamp:</span> <span class="value">`)
	sb.WriteString(html.EscapeString(time.Now().UTC().Format(time.RFC3339)))
	sb.WriteString(`</span></div>`)

	sb.WriteString(`</div>
</div>
</body>
</html>`)

	return sb.String()
}
