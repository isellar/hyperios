package scheduler

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/isellar/hyperios/internal/events"
)

func TestScheduler_RegisterAndRun(t *testing.T) {
	n := events.NewNotifier(16)
	s := New(n)
	s.Start()
	defer s.Stop()

	var count int32
	// Every second
	err := s.Register("test-job", "* * * * * *", func() {
		atomic.AddInt32(&count, 1)
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Wait up to 3 seconds for at least one execution
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&count) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if atomic.LoadInt32(&count) == 0 {
		t.Error("expected job to run at least once within 3 seconds")
	}
}

func TestScheduler_PublishesEvent(t *testing.T) {
	n := events.NewNotifier(16)
	ch := n.Events()
	s := New(n)
	s.Start()
	defer s.Stop()

	_ = s.Register("event-test", "* * * * * *", func() {})

	select {
	case e := <-ch:
		if e.Kind != events.EventScheduledFired {
			t.Errorf("expected EventScheduledFired, got %q", e.Kind)
		}
		if e.Payload != "event-test" {
			t.Errorf("expected payload 'event-test', got %v", e.Payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for scheduled event")
	}
}

func TestScheduler_DuplicateRegistration(t *testing.T) {
	s := New(nil)
	err := s.Register("job1", "* * * * * *", func() {})
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err = s.Register("job1", "* * * * * *", func() {})
	if err == nil {
		t.Error("expected error on duplicate job registration")
	}
}

func TestScheduler_Unregister(t *testing.T) {
	s := New(nil)
	s.Start()
	defer s.Stop()

	var count int32
	_ = s.Register("removable", "* * * * * *", func() {
		atomic.AddInt32(&count, 1)
	})

	time.Sleep(1500 * time.Millisecond)
	s.Unregister("removable")
	countAfterUnregister := atomic.LoadInt32(&count)

	time.Sleep(1500 * time.Millisecond)
	countAfterWait := atomic.LoadInt32(&count)

	if countAfterWait > countAfterUnregister {
		t.Errorf("job ran after unregister: count went from %d to %d", countAfterUnregister, countAfterWait)
	}
}

func TestScheduler_InvalidCronExpr(t *testing.T) {
	s := New(nil)
	err := s.Register("bad-job", "not a cron expression", func() {})
	if err == nil {
		t.Error("expected error for invalid cron expression")
	}
}

func TestScheduler_Jobs(t *testing.T) {
	s := New(nil)
	_ = s.Register("j1", "0 * * * * *", func() {})
	_ = s.Register("j2", "0 * * * * *", func() {})

	jobs := s.Jobs()
	if len(jobs) != 2 {
		t.Errorf("expected 2 jobs, got %d", len(jobs))
	}
}
