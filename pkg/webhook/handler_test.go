package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockEmailProvider implements email.Provider for testing.
type mockEmailProvider struct {
	sendCalled bool
	lastTo     string
	lastSubj   string
	err        error
}

func (m *mockEmailProvider) Send(_ context.Context, to, subject, _ string) error {
	m.sendCalled = true
	m.lastTo = to
	m.lastSubj = subject
	return m.err
}

func TestVerifySignature(t *testing.T) {
	secret := "test-secret"
	payload := []byte(`{"test": "data"}`)

	// Compute valid signature
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	tests := []struct {
		name      string
		payload   []byte
		signature string
		secret    string
		want      bool
	}{
		{
			name:      "valid signature",
			payload:   payload,
			signature: validSig,
			secret:    secret,
			want:      true,
		},
		{
			name:      "invalid signature",
			payload:   payload,
			signature: "sha256=invalid",
			secret:    secret,
			want:      false,
		},
		{
			name:      "empty secret",
			payload:   payload,
			signature: validSig,
			secret:    "",
			want:      false,
		},
		{
			name:      "missing sha256 prefix",
			payload:   payload,
			signature: "invalid",
			secret:    secret,
			want:      false,
		},
		{
			name:      "empty signature",
			payload:   payload,
			signature: "",
			secret:    secret,
			want:      false,
		},
		{
			name:      "wrong payload",
			payload:   []byte("different"),
			signature: validSig,
			secret:    secret,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := verifySignature(tt.payload, tt.signature, tt.secret)
			if got != tt.want {
				t.Errorf("verifySignature() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandler_ServeHTTP_MethodNotAllowed(t *testing.T) {
	h := NewHandler("secret", &mockEmailProvider{}, slog.New(slog.DiscardHandler))

	req := httptest.NewRequest(http.MethodGet, "/webhook", http.NoBody)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("got status %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandler_ServeHTTP_NonMarketplaceEvent(t *testing.T) {
	mp := &mockEmailProvider{}
	h := NewHandler("secret", mp, slog.New(slog.DiscardHandler))

	req := httptest.NewRequest(http.MethodPost, "/webhook", http.NoBody)
	req.Header.Set("X-GitHub-Event", "push") //nolint:canonicalheader // GitHub header
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}
	if mp.sendCalled {
		t.Error("non-marketplace events (except ping) should not send email")
	}
}

func TestHandler_ServeHTTP_PingEvent(t *testing.T) {
	mp := &mockEmailProvider{}
	h := NewHandler("secret", mp, slog.New(slog.DiscardHandler))

	req := httptest.NewRequest(http.MethodPost, "/webhook", http.NoBody)
	req.Header.Set("X-GitHub-Event", "ping")                      //nolint:canonicalheader // GitHub header
	req.Header.Set("X-GitHub-Delivery", "ping-delivery-id-12345") //nolint:canonicalheader // GitHub header
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "pong" {
		t.Errorf("got body %q, want %q", w.Body.String(), "pong")
	}
	if !mp.sendCalled {
		t.Error("ping events should send notification email")
	}
	if mp.lastTo != notifyEmail {
		t.Errorf("got to=%q, want %q", mp.lastTo, notifyEmail)
	}
	if !strings.Contains(mp.lastSubj, "ping") {
		t.Errorf("subject %q should contain 'ping'", mp.lastSubj)
	}
}

func TestHandler_ServeHTTP_PayloadTooLarge(t *testing.T) {
	h := NewHandler("secret", &mockEmailProvider{}, slog.New(slog.DiscardHandler))

	req := httptest.NewRequest(http.MethodPost, "/webhook", http.NoBody)
	req.Header.Set("X-GitHub-Event", "marketplace_purchase") //nolint:canonicalheader // GitHub header
	req.ContentLength = maxPayloadSize + 1
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("got status %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandler_ServeHTTP_InvalidSignature(t *testing.T) {
	h := NewHandler("secret", &mockEmailProvider{}, slog.New(slog.DiscardHandler))

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(`{}`))
	req.Header.Set("X-GitHub-Event", "marketplace_purchase") //nolint:canonicalheader // GitHub header
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandler_ServeHTTP_InvalidJSON(t *testing.T) {
	secret := "test-secret"
	payload := []byte(`{invalid json}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	h := NewHandler(secret, &mockEmailProvider{}, slog.New(slog.DiscardHandler))

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(payload)))
	req.Header.Set("X-GitHub-Event", "marketplace_purchase") //nolint:canonicalheader // GitHub header
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandler_ServeHTTP_Success(t *testing.T) {
	secret := "test-secret"
	event := MarketplaceEvent{
		Action:        "purchased",
		EffectiveDate: "2024-01-15",
		MarketplacePurchase: MarketplacePurchase{
			Account: Account{
				Login: "testuser",
				Email: "test@example.com",
				Type:  "User",
			},
			Plan: Plan{
				Name: "Pro",
			},
			BillingCycle: "monthly",
			UnitCount:    5,
		},
		Sender: Sender{
			Login: "admin",
			ID:    123,
		},
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal event: %v", err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	mp := &mockEmailProvider{}
	h := NewHandler(secret, mp, slog.New(slog.DiscardHandler))

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(payload)))
	req.Header.Set("X-GitHub-Event", "marketplace_purchase") //nolint:canonicalheader // GitHub header
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-GitHub-Delivery", "test-delivery-id") //nolint:canonicalheader // GitHub header
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}
	if !mp.sendCalled {
		t.Error("expected email to be sent")
	}
	if mp.lastTo != notifyEmail {
		t.Errorf("got to=%q, want %q", mp.lastTo, notifyEmail)
	}
	if !strings.Contains(mp.lastSubj, "purchased") {
		t.Errorf("subject %q should contain 'purchased'", mp.lastSubj)
	}
}

func TestFormatEmailBody(t *testing.T) {
	event := &MarketplaceEvent{
		Action:        "purchased",
		EffectiveDate: "2024-01-15",
		MarketplacePurchase: MarketplacePurchase{
			Account: Account{
				Login: "testuser",
				Email: "test@example.com",
				Type:  "Organization",
			},
			Plan: Plan{
				Name: "Enterprise",
			},
			BillingCycle: "yearly",
			UnitCount:    10,
		},
		Sender: Sender{
			Login: "admin",
		},
	}

	body := formatEmailBody(event)

	// Check that key fields are present
	checks := []string{
		"purchased",
		"testuser",
		"Organization",
		"test@example.com",
		"Enterprise",
		"yearly",
		"10",
		"admin",
		"2024-01-15",
		"action-purchased", // CSS class
	}

	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Errorf("body should contain %q", check)
		}
	}
}

func TestFormatEmailBody_EmptyAccountType(t *testing.T) {
	event := &MarketplaceEvent{
		Action: "purchased",
		MarketplacePurchase: MarketplacePurchase{
			Account: Account{
				Login: "testuser",
				Type:  "", // Empty type
			},
			Plan: Plan{Name: "Free"},
		},
		Sender: Sender{Login: "admin"},
	}

	body := formatEmailBody(event)

	if !strings.Contains(body, "Unknown") {
		t.Error("empty account type should default to 'Unknown'")
	}
}

func TestFormatEmailBody_OptionalFields(t *testing.T) {
	// Test with minimal fields (no email, no billing cycle, no unit count, no effective date)
	event := &MarketplaceEvent{
		Action: "cancelled",
		MarketplacePurchase: MarketplacePurchase{
			Account: Account{
				Login: "testuser",
				Type:  "User",
			},
			Plan: Plan{Name: "Free"},
		},
		Sender: Sender{Login: "testuser"},
	}

	body := formatEmailBody(event)

	// Should not contain optional fields
	if strings.Contains(body, "Account Email") {
		t.Error("should not contain Account Email when empty")
	}
	if strings.Contains(body, "Billing Cycle") {
		t.Error("should not contain Billing Cycle when empty")
	}
	if strings.Contains(body, "Units") {
		t.Error("should not contain Units when zero")
	}
	if strings.Contains(body, "Effective Date") {
		t.Error("should not contain Effective Date when empty")
	}
}

func TestFormatEmailBody_HTMLEscaping(t *testing.T) {
	event := &MarketplaceEvent{
		Action: "<script>alert('xss')</script>",
		MarketplacePurchase: MarketplacePurchase{
			Account: Account{
				Login: "<b>evil</b>",
				Type:  "User",
			},
			Plan: Plan{Name: "Free"},
		},
		Sender: Sender{Login: "admin"},
	}

	body := formatEmailBody(event)

	if strings.Contains(body, "<script>") {
		t.Error("HTML should be escaped")
	}
	if strings.Contains(body, "<b>evil</b>") {
		t.Error("HTML should be escaped")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("should contain escaped HTML")
	}
}

func TestFormatPingEmailBody(t *testing.T) {
	body := formatPingEmailBody("delivery-123", "192.168.1.1:54321")

	checks := []string{
		"delivery-123",
		"192.168.1.1:54321",
		"Webhook Ping",
		"Delivery ID",
		"Remote Address",
		"Timestamp",
	}

	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Errorf("body should contain %q", check)
		}
	}
}

func TestFormatPingEmailBody_HTMLEscaping(t *testing.T) {
	body := formatPingEmailBody("<script>evil</script>", "<b>bad</b>")

	if strings.Contains(body, "<script>") {
		t.Error("HTML should be escaped in delivery ID")
	}
	if strings.Contains(body, "<b>bad</b>") {
		t.Error("HTML should be escaped in remote addr")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("should contain escaped HTML")
	}
}
