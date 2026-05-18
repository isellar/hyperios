package router

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/isellar/hyperios/internal/arbiter"
	"github.com/isellar/hyperios/internal/bus"
	"github.com/isellar/hyperios/internal/cache"
	"github.com/isellar/hyperios/internal/capability"
	"github.com/isellar/hyperios/internal/executor"
	"github.com/isellar/hyperios/internal/types"
)

// PipelineRunner is the signature for the full pipeline fallback.
type PipelineRunner func(intent, workspaceDir string) error

// Config holds IntentRouter configuration.
type Config struct {
	CachePath    string
	TemplatePath string
	StatsPath    string
	Fallback     PipelineRunner
	Registry     *capability.Registry
	Validator    *capability.CommandValidator
	EventBus     *bus.Bus
	SessionID    string
	AutonomyLevel int
	WorkspaceDir string
}

// IntentRouter routes intents to the fastest appropriate execution path.
type IntentRouter struct {
	cache    *cache.PlanCache
	registry *TemplateRegistry
	stats    *StatsManager
	fallback PipelineRunner
	cfg      Config
}

// New creates a new IntentRouter.
func New(cfg Config) *IntentRouter {
	r := &IntentRouter{
		cache:    cache.New(cache.Config{Path: cfg.CachePath}),
		stats:    NewStatsManager(cfg.StatsPath),
		fallback: cfg.Fallback,
		cfg:      cfg,
	}

	registry, err := NewTemplateRegistry(cfg.TemplatePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to load templates: %v\n", err)
		registry = &TemplateRegistry{}
	}
	r.registry = registry

	return r
}

// Route processes an intent through the fastest available path.
func (r *IntentRouter) Route(ctx context.Context, intent string) error {
	start := time.Now()

	tier := r.stats.Tier(intent)

	switch tier {
	case TierTrusted:
		return r.executeTrusted(ctx, intent)
	case TierTemplated:
		return r.executeTemplated(ctx, intent, start)
	case TierCached:
		return r.executeCached(ctx, intent, start)
	default:
		return r.executeNovel(ctx, intent, start)
	}
}

// Cache returns the underlying plan cache for external recording.
func (r *IntentRouter) Cache() *cache.PlanCache {
	return r.cache
}

// Stats returns the underlying stats manager.
func (r *IntentRouter) Stats() *StatsManager {
	return r.stats
}

func (r *IntentRouter) executeNovel(ctx context.Context, intent string, start time.Time) error {
	if r.fallback == nil {
		return fmt.Errorf("no fallback pipeline configured")
	}

	err := r.fallback(intent, r.cfg.WorkspaceDir)
	duration := time.Since(start)

	if err == nil {
		r.stats.RecordExecution(intent, duration, true, "")
	} else {
		r.stats.RecordExecution(intent, duration, false, err.Error())
	}

	return err
}

func (r *IntentRouter) executeCached(ctx context.Context, intent string, start time.Time) error {
	cached, ok := r.cache.Get(intent)
	if !ok {
		return r.executeNovel(ctx, intent, start)
	}

	if !cached.GuardCheck() {
		r.cache.Remove(intent)
		return r.executeNovel(ctx, intent, start)
	}

	verdicts := r.runArbiter(cached.Plan)
	if hasBlocked(verdicts) {
		return fmt.Errorf("cached plan blocked by arbiter")
	}

	err := r.executePlan(ctx, cached.Plan, verdicts)
	duration := time.Since(start)

	if err == nil {
		r.cache.RecordSuccess(intent)
		r.stats.RecordExecution(intent, duration, true, "")
	} else {
		r.cache.RecordFailure(intent)
		r.stats.RecordExecution(intent, duration, false, err.Error())
	}

	return err
}

func (r *IntentRouter) executeTemplated(ctx context.Context, intent string, start time.Time) error {
	tmpl, slots := r.registry.Match(intent)
	if tmpl == nil {
		return r.executeNovel(ctx, intent, start)
	}

	plan := r.registry.Fill(tmpl, slots)

	guards := r.buildGuards(plan)
	r.cache.Store(intent, plan, guards)

	verdicts := r.runArbiter(plan)
	if hasBlocked(verdicts) {
		return fmt.Errorf("templated plan blocked by arbiter")
	}

	err := r.executePlan(ctx, plan, verdicts)
	duration := time.Since(start)

	if err == nil {
		r.cache.RecordSuccess(intent)
		r.stats.RecordExecution(intent, duration, true, "")
	} else {
		r.cache.RecordFailure(intent)
		r.stats.RecordExecution(intent, duration, false, err.Error())
	}

	return err
}

func (r *IntentRouter) executeTrusted(ctx context.Context, intent string) error {
	tmpl, slots := r.registry.Match(intent)
	if tmpl == nil {
		cached, ok := r.cache.Get(intent)
		if !ok {
			return r.executeNovel(ctx, intent, time.Now())
		}
		if !cached.GuardCheck() {
			r.cache.Remove(intent)
			return r.executeNovel(ctx, intent, time.Now())
		}
		return r.executePlan(ctx, cached.Plan, nil)
	}

	plan := r.registry.Fill(tmpl, slots)
	return r.executePlan(ctx, plan, nil)
}

func (r *IntentRouter) runArbiter(plan *types.ActionPlan) []types.ArbiterVerdict {
	emptyReport := &types.RiskReport{}
	policyArbiter := arbiter.NewWithLevel(r.cfg.AutonomyLevel)
	return policyArbiter.Decide(plan, emptyReport)
}

func (r *IntentRouter) executePlan(ctx context.Context, plan *types.ActionPlan, verdicts []types.ArbiterVerdict) error {
	if r.cfg.AutonomyLevel == 0 {
		return nil
	}

	execCfg := executor.ExecutorConfig{
		Registry:     r.cfg.Registry,
		Workspace:    r.cfg.WorkspaceDir,
		ExecutorType: types.ExecutorLocal,
		Bus:          r.cfg.EventBus,
		SessionID:    r.cfg.SessionID,
	}
	execInstance := executor.New(execCfg)

	verdictMap := make(map[string]*types.ArbiterVerdict)
	for i := range verdicts {
		verdictMap[verdicts[i].StepID] = &verdicts[i]
	}

	for _, step := range plan.Steps {
		v, ok := verdictMap[step.ID]
		if !ok {
			continue
		}

		if v.Verdict == "blocked" {
			continue
		}

		if v.Verdict == "modified" {
			return fmt.Errorf("step %s requires approval — not supported in fast path", step.ID)
		}

		if r.cfg.Validator != nil {
			vr := r.cfg.Validator.Validate(step)
			if !vr.Valid {
				continue
			}
		}

		_, execErr := execInstance.Execute(ctx, step)
		if execErr != nil {
			return execErr
		}
	}

	return nil
}

func (r *IntentRouter) buildGuards(plan *types.ActionPlan) []cache.Guard {
	var guards []cache.Guard

	for _, step := range plan.Steps {
		if len(step.Command) == 0 {
			continue
		}

		binary := step.Command[0]
		guards = append(guards, cache.Guard{
			Check: func() bool {
				return binaryExists(binary)
			},
			Description: fmt.Sprintf("%s is available", binary),
		})
	}

	return guards
}

func binaryExists(name string) bool {
	if runtime.GOOS == "windows" {
		if !strings.Contains(name, ".") {
			name = name + ".exe"
		}
	}
	_, err := exec.LookPath(name)
	return err == nil
}

func hasBlocked(verdicts []types.ArbiterVerdict) bool {
	for _, v := range verdicts {
		if v.Verdict == "blocked" {
			return true
		}
	}
	return false
}

// CacheDir returns the appropriate cache directory for the current OS.
func CacheDir(dataPathFn func(string) string) string {
	if dataPathFn != nil {
		return dataPathFn("cache")
	}

	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), "hyperi", "cache")
	}
	return filepath.Join(os.Getenv("HOME"), ".local", "share", "hyperi", "cache")
}
