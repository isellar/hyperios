// Package scheduler manages in-process recurring tasks for HyperiOS.
//
// It wraps robfig/cron with a named job registry and event bus integration.
// Scheduled jobs publish EventScheduledFired to the bus when they run.
//
// This scheduler handles agent-internal cadence: manifest re-scan, session
// cleanup, audit log rotation. For user-directed recurring tasks that must
// survive process restarts, use execute:schedule:systemd (systemd timers).
package scheduler

import (
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/isellar/hyperios/internal/bus"
)

// Job is a registered recurring task.
type Job struct {
	Name     string
	CronExpr string
	EntryID  cron.EntryID
}

// Scheduler wraps robfig/cron with a named job registry.
type Scheduler struct {
	mu    sync.RWMutex
	cron  *cron.Cron
	jobs  map[string]*Job
	bus   *bus.Bus
}

// New creates a Scheduler. If eventBus is nil, events are not published.
func New(eventBus *bus.Bus) *Scheduler {
	return &Scheduler{
		cron: cron.New(cron.WithSeconds()),
		jobs: make(map[string]*Job),
		bus:  eventBus,
	}
}

// Register adds a named recurring job. cronExpr supports the standard 5-field
// or 6-field (with seconds) cron syntax. Returns an error if the name is
// already registered or the expression is invalid.
func (s *Scheduler) Register(name, cronExpr string, fn func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.jobs[name]; exists {
		return fmt.Errorf("scheduler: job %q already registered", name)
	}

	eventBus := s.bus
	jobName := name

	id, err := s.cron.AddFunc(cronExpr, func() {
		if eventBus != nil {
			eventBus.Publish(bus.Event{
				Kind:      bus.EventScheduledFired,
				SessionID: "",
				Payload:   jobName,
				Timestamp: time.Now(),
			})
		}
		fn()
	})
	if err != nil {
		return fmt.Errorf("scheduler: invalid cron expression %q for job %q: %w", cronExpr, name, err)
	}

	s.jobs[name] = &Job{
		Name:     name,
		CronExpr: cronExpr,
		EntryID:  id,
	}

	return nil
}

// Unregister removes a named job. No-op if the job does not exist.
func (s *Scheduler) Unregister(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs[name]
	if !ok {
		return
	}
	s.cron.Remove(job.EntryID)
	delete(s.jobs, name)
}

// Start begins running scheduled jobs. Should be called once at startup.
func (s *Scheduler) Start() {
	s.cron.Start()
}

// Stop gracefully stops the scheduler, waiting for running jobs to complete.
func (s *Scheduler) Stop() {
	s.cron.Stop()
}

// Jobs returns a snapshot of all registered jobs.
func (s *Scheduler) Jobs() []Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	jobs := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, *j)
	}
	return jobs
}

// DefaultJobs registers the built-in HyperiOS scheduler jobs.
// Called at startup after the scheduler is created.
func (s *Scheduler) DefaultJobs(
	manifestRescan func(),
	sessionCleanup func(),
	auditRotation func(),
) {
	// Manifest re-scan every 6 hours
	_ = s.Register("manifest:rescan", "0 0 */6 * * *", manifestRescan)

	// Session cleanup daily at 3am (remove sessions older than 30 days)
	_ = s.Register("session:cleanup", "0 0 3 * * *", sessionCleanup)

	// Audit log rotation weekly on Sunday at 2am
	_ = s.Register("audit:rotate", "0 0 2 * * 0", auditRotation)
}
