// Package module defines the Module interface pattern for HyperiOS self-improving components.
// Each module implements Report, Tune, and Health to participate in the self-tuning loop.
package module

import (
	"context"
	"time"
)

// Module is the interface for self-improving components.
type Module interface {
	Name() string
	Report(ctx context.Context, window time.Duration) (ModuleReport, error)
	Tune(ctx context.Context, change TuningChange) error
	Health() ModuleHealth
}

// ModuleReport contains metrics and issues for a module within a time window.
type ModuleReport struct {
	ModuleName string
	Window     time.Duration
	Metrics    map[string]any
	Issues     []string
}

// TuningChange represents a parameter adjustment request for a module.
type TuningChange struct {
	Module string
	Path   string
	Value  any
}

// ModuleHealth reports the current health status of a module.
type ModuleHealth struct {
	Status    string // "healthy", "degraded", "unhealthy"
	Details   string
	Timestamp time.Time
}
