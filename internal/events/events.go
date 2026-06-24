// Package events provides a lightweight event system for HyperiOS.
// It replaces the previous event bus with direct function calls via callbacks.
// Events flow from producers (executor, scheduler) to consumers (TUI, audit)
// through a single Notifier channel or direct callback functions.
package events

import (
	"time"
)

// EventKind identifies the type of an event.
type EventKind string

const (
	EventStepStarted     EventKind = "step:started"
	EventStepCompleted   EventKind = "step:completed"
	EventStepFailed      EventKind = "step:failed"
	EventStepSkipped     EventKind = "step:skipped"
	EventPlanCompleted   EventKind = "plan:completed"
	EventPlanFailed      EventKind = "plan:failed"
	EventScheduledFired  EventKind = "scheduled:fired"
	EventAlertTriggered  EventKind = "alert:triggered"
	EventApprovalNeeded  EventKind = "approval:needed"
	EventManifestUpdated EventKind = "manifest:updated"
	EventSessionResumed  EventKind = "session:resumed"
)

// ApprovalPayload is the Payload for EventApprovalNeeded events.
// The pipeline blocks on ReplyCh waiting for a response.
type ApprovalPayload struct {
	StepID         string
	StepDesc       string
	Command        []string
	Reason         string
	TimeoutSeconds int
	// ReplyCh receives true (approved) or false (denied/timeout).
	// It is closed after the first response; subsequent sends are no-ops.
	ReplyCh chan bool
}

// Respond sends an approval response. Safe to call multiple times; only the
// first response takes effect.
func (a *ApprovalPayload) Respond(approved bool) {
	defer func() { recover() }()
	a.ReplyCh <- approved
	close(a.ReplyCh)
}

// Event is a single message.
type Event struct {
	Kind       EventKind
	SessionID  string
	StepID     string
	Payload    any
	Timestamp  time.Time
	Background bool
}

// Handler is a callback function for processing events.
// Components call this directly instead of publishing to a bus.
type Handler func(Event)

// Notifier is a simple event sink backed by a single buffered channel.
// It replaces the fan-out bus with a direct channel for the TUI consumer.
// An optional audit callback is invoked synchronously on every publish.
type Notifier struct {
	ch         chan Event
	auditFn    func(sessionID, stage string, input, output any) error
}

// NewNotifier creates a Notifier with the given buffer size.
func NewNotifier(bufSize int) *Notifier {
	if bufSize <= 0 {
		bufSize = 256
	}
	return &Notifier{ch: make(chan Event, bufSize)}
}

// SetAuditCallback registers a function that is called synchronously on every Publish.
// This allows the audit log to receive all events without consuming the TUI channel.
func (n *Notifier) SetAuditCallback(fn func(sessionID, stage string, input, output any) error) {
	n.auditFn = fn
}

// Publish sends an event to the channel. Non-blocking; drops if full.
// Also invokes the audit callback if one is registered.
func (n *Notifier) Publish(e Event) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	if n.auditFn != nil {
		_ = n.auditFn(e.SessionID, string(e.Kind), e.StepID, e.Payload)
	}
	select {
	case n.ch <- e:
	default:
	}
}

// PublishStep is a convenience helper for step-lifecycle events.
func (n *Notifier) PublishStep(kind EventKind, sessionID, stepID string, payload any) {
	n.Publish(Event{
		Kind:      kind,
		SessionID: sessionID,
		StepID:    stepID,
		Payload:   payload,
		Timestamp: time.Now(),
	})
}

// Events returns the read-only channel for consuming events.
func (n *Notifier) Events() <-chan Event {
	return n.ch
}

// Close closes the event channel.
func (n *Notifier) Close() {
	close(n.ch)
}

// DrainToAudit reads events from the notifier channel and writes them to the
// provided audit function. Runs until the channel is closed. Call in a goroutine.
func DrainToAudit(ch <-chan Event, auditFn func(sessionID, stage string, input, output any) error) {
	for e := range ch {
		_ = auditFn(e.SessionID, string(e.Kind), e.StepID, e.Payload)
	}
}

// NewHandler returns a Handler that publishes to the given Notifier.
// Returns nil if n is nil.
func NewHandler(n *Notifier) Handler {
	if n == nil {
		return nil
	}
	return n.Publish
}
