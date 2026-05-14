package bus

import (
	"testing"
	"time"
)

func TestBus_PublishSubscribe(t *testing.T) {
	b := New(16)
	ch := b.Subscribe()

	b.Publish(Event{Kind: EventStepStarted, SessionID: "s1", StepID: "step1"})

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

func TestBus_FanOut(t *testing.T) {
	b := New(16)
	ch1 := b.Subscribe()
	ch2 := b.Subscribe()

	b.Publish(Event{Kind: EventPlanCompleted, SessionID: "s1"})

	for _, ch := range []<-chan Event{ch1, ch2} {
		select {
		case e := <-ch:
			if e.Kind != EventPlanCompleted {
				t.Errorf("expected EventPlanCompleted, got %q", e.Kind)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("timed out waiting for fan-out event")
		}
	}
}

func TestBus_Unsubscribe(t *testing.T) {
	b := New(16)
	ch := b.Subscribe()
	b.Unsubscribe(ch)

	// Publishing after unsubscribe should not panic
	b.Publish(Event{Kind: EventStepCompleted, SessionID: "s1"})
}

func TestBus_FullBuffer_DoesNotBlock(t *testing.T) {
	b := New(2) // tiny buffer
	ch := b.Subscribe()
	_ = ch // don't drain it

	// Fill buffer + overflow — should not block
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			b.Publish(Event{Kind: EventStepStarted, SessionID: "s1"})
		}
		close(done)
	}()

	select {
	case <-done:
		// success — did not block
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Publish blocked on full subscriber buffer")
	}
}

func TestBus_PublishStep(t *testing.T) {
	b := New(16)
	ch := b.Subscribe()

	b.PublishStep(EventStepFailed, "sess1", "s3", "something went wrong")

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

func TestApprovalPayload_RespondTwice(t *testing.T) {
	ap := &ApprovalPayload{
		StepID:  "s1",
		ReplyCh: make(chan bool, 1),
	}

	// First respond should work
	ap.Respond(true)

	// Second respond should not panic (channel is closed)
	ap.Respond(false)

	if result := <-ap.ReplyCh; !result {
		t.Error("expected true from first response")
	}
}

func TestBus_Close(t *testing.T) {
	b := New(16)
	ch := b.Subscribe()
	b.Close()

	// Channel should be closed
	_, open := <-ch
	if open {
		t.Error("expected channel to be closed after Bus.Close()")
	}
}
