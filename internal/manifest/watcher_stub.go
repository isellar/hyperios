//go:build !linux

package manifest

import "github.com/isellar/hyperios/internal/bus"

// Watcher is a no-op stub on non-Linux platforms.
// HyperiOS targets Linux only; this stub exists to keep the package
// compilable on the developer's host machine (Windows/macOS).
type Watcher struct{}

// NewWatcher returns a no-op watcher on non-Linux platforms.
func NewWatcher(store *Store, eventBus *bus.Bus, watchPaths []string) (*Watcher, error) {
	return &Watcher{}, nil
}

// Start is a no-op on non-Linux platforms.
func (w *Watcher) Start() {}

// Stop is a no-op on non-Linux platforms.
func (w *Watcher) Stop() {}
