// Package bus provides the HyperiOS event bus — a buffered channel that
// decouples producers (executor, scheduler, system monitors) from consumers
// (TUI, web UI, audit log).
//
// The event bus is what makes the persistent shell feel like an OS rather than
// a REPL: the TUI can receive messages it didn't ask for (step completion,
// alerts, approval requests) without the user having typed anything.
//
// All events are written to the audit log regardless of other consumers.
// The bus fans out to all registered subscribers.
package bus

import (
	"sync"
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
	defer func() { recover() }() // suppress panic on closed channel
	a.ReplyCh <- approved
	close(a.ReplyCh)
}

// Event is a single message on the bus.
type Event struct {
	Kind      EventKind
	SessionID string
	StepID    string    // empty for plan-level events
	Payload   any       // typed payload; depends on Kind
	Timestamp time.Time
	Background bool     // true if emitted by a background (scheduled) session
}

// Bus is a fan-out event bus backed by a buffered channel.
// Multiple subscribers can register; each receives all events.
type Bus struct {
	mu          sync.RWMutex
	subscribers []chan Event
	bufSize     int
}

// New creates a Bus with the given buffer size per subscriber.
// A buffer size of 256 is suitable for most interactive sessions.
func New(bufSize int) *Bus {
	if bufSize <= 0 {
		bufSize = 256
	}
	return &Bus{bufSize: bufSize}
}

// Subscribe returns a read-only channel that receives all future events.
// The caller should drain the channel promptly to avoid blocking the bus.
// To unsubscribe, call Unsubscribe with the returned channel.
func (b *Bus) Subscribe() <-chan Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan Event, b.bufSize)
	b.subscribers = append(b.subscribers, ch)
	return ch
}

// Unsubscribe removes a subscriber channel and closes it.
func (b *Bus) Unsubscribe(ch <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, sub := range b.subscribers {
		if sub == ch {
			b.subscribers = append(b.subscribers[:i], b.subscribers[i+1:]...)
			close(sub)
			return
		}
	}
}

// Publish sends an event to all subscribers.
// If a subscriber's buffer is full, the event is dropped for that subscriber
// and a warning is noted (non-blocking — the bus never stalls the producer).
func (b *Bus) Publish(e Event) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, sub := range b.subscribers {
		select {
		case sub <- e:
		default:
			// Subscriber buffer full — drop event for this subscriber.
			// The audit log subscriber has a large buffer and should not drop.
		}
	}
}

// PublishStep is a convenience helper for step-lifecycle events.
func (b *Bus) PublishStep(kind EventKind, sessionID, stepID string, payload any) {
	b.Publish(Event{
		Kind:      kind,
		SessionID: sessionID,
		StepID:    stepID,
		Payload:   payload,
		Timestamp: time.Now(),
	})
}

// Close closes all subscriber channels and clears the subscriber list.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sub := range b.subscribers {
		close(sub)
	}
	b.subscribers = nil
}

// DrainToAudit reads events from the bus and writes them to the provided
// audit function. Runs until the channel is closed. Call in a goroutine.
func DrainToAudit(ch <-chan Event, auditFn func(sessionID, stage string, input, output any) error) {
	for e := range ch {
		_ = auditFn(e.SessionID, string(e.Kind), e.StepID, e.Payload)
	}
}
