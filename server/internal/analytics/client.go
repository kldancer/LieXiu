// Package analytics defines local event facts consumed by Prometheus metrics.
// It does not transmit product or diagnostic data to an external service.
package analytics

import "time"

// Event is a local semantic event used to update bounded Prometheus counters.
type Event struct {
	// Name of the event (e.g. "signup", "workspace_created").
	Name string

	// DistinctID identifies the actor for local event construction. Metric
	// dispatchers must never use it as a label.
	DistinctID string

	// WorkspaceID scopes the event to a workspace. Required when the event is
	// about a workspace-level action (workspace_created, issue_executed, ...).
	// Empty is allowed for pre-workspace events (signup).
	WorkspaceID string

	// Properties is the free-form bag of event attributes. Only serialisable
	// values (string, number, bool, nested maps/slices of the same) should
	// go here. Never put raw PII like full emails here — use email_domain.
	Properties map[string]any

	// SetOnce is retained in the transitional event contract while legacy event
	// builders are removed in later Wave 1C units. It is never transmitted.
	SetOnce map[string]any

	// Set is retained for compatibility with existing local metric builders and
	// is never transmitted.
	Set map[string]any

	// Timestamp is optional metadata for local consumers.
	Timestamp time.Time
}

// Client is a transitional compatibility surface for existing constructors.
// The only implementation is NoopClient; external analytics transport was
// removed in Wave 1C.4.
type Client interface {
	Capture(e Event)
	// Close drains pending events. Call once during graceful shutdown.
	Close()
}

// NewFromEnv retains the old constructor signature while always returning a
// local no-op sink. No environment variable can enable external transmission.
func NewFromEnv() Client {
	return NoopClient{}
}

// NoopClient silently drops compatibility captures.
type NoopClient struct{}

func (NoopClient) Capture(Event) {}
func (NoopClient) Close()        {}
