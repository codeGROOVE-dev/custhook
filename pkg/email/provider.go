// Package email handles sending notification emails via Brevo or mock.
package email

import (
	"context"
)

// Provider defines the interface for email sending implementations.
type Provider interface {
	Send(ctx context.Context, to, subject, body string) error
}
