package events

import (
	"testing"
	"time"
)

func TestNotifier_PublishReceive(t *testing.T) {
	n := NewNotifier(16)
	ch := n.Events()

	n.Publish(Event{Kind: EventStepStarted, SessionID: "s1", StepID: "step1"})

	select {
	case e := <-ch:
		if e.Kind != EventStepStarted {
			t.Errorf("expected EventStepStarted, got %q", e.Kind)
		}
		if e.SessionID != "s1" {
			t.Errorf("expected session s1, got %q", e.SessionID)
		}
		if e.Timestamp.IsZero() {
			t.Error("expected timestamp to be set")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}
}

func TestNotifier_PublishStep(t *testing.T) {
	n := NewNotifier(16)
	ch := n.Events()

	n.PublishStep(EventStepFailed, "sess1", "s3", "something went wrong")

	select {
	case e := <-ch:
		if e.StepID != "s3" {
			t.Errorf("expected step s3, got %q", e.StepID)
		}
		if e.Payload != "something went wrong" {
			t.Errorf("unexpected payload: %v", e.Payload)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out")
	}
}

func TestNotifier_FullBuffer_DoesNotBlock(t *testing.T) {
	n := NewNotifier(2)
	_ = n.Events()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			n.Publish(Event{Kind: EventStepStarted, SessionID: "s1"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Publish blocked on full buffer")
	}
}

func TestNotifier_Close(t *testing.T) {
	n := NewNotifier(16)
	ch := n.Events()
	n.Close()

	_, open := <-ch
	if open {
		t.Error("expected channel to be closed after Notifier.Close()")
	}
}

func TestApprovalPayload_RespondTwice(t *testing.T) {
	ap := &ApprovalPayload{
		StepID:  "s1",
		ReplyCh: make(chan bool, 1),
	}

	ap.Respond(true)
	ap.Respond(false)

	if result := <-ap.ReplyCh; !result {
		t.Error("expected true from first response")
	}
}

func TestNotifier_AuditCallback(t *testing.T) {
	n := NewNotifier(16)

	var capturedSessionID, capturedStage string
	n.SetAuditCallback(func(sessionID, stage string, input, output any) error {
		capturedSessionID = sessionID
		capturedStage = stage
		return nil
	})

	n.Publish(Event{Kind: EventStepCompleted, SessionID: "s1"})

	if capturedSessionID != "s1" {
		t.Errorf("expected session s1, got %q", capturedSessionID)
	}
	if capturedStage != string(EventStepCompleted) {
		t.Errorf("expected stage %q, got %q", EventStepCompleted, capturedStage)
	}
}

func TestNewHandler(t *testing.T) {
	n := NewNotifier(16)
	handler := NewHandler(n)
	if handler == nil {
		t.Fatal("expected non-nil handler")
	}

	ch := n.Events()
	handler(Event{Kind: EventStepStarted, SessionID: "s1"})

	select {
	case e := <-ch:
		if e.Kind != EventStepStarted {
			t.Errorf("expected EventStepStarted, got %q", e.Kind)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out")
	}
}

func TestNewHandler_Nil(t *testing.T) {
	handler := NewHandler(nil)
	if handler != nil {
		t.Error("expected nil handler for nil notifier")
	}
}
