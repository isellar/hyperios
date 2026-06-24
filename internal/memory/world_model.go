package memory

import (
	"os"
	"runtime"
	"sync"
)

// WorldModel is the agent's working knowledge of the current system state.
// Facts are keyed by arbitrary strings and can hold any value.
// The model is pre-populated with basic OS facts at construction time.
type WorldModel struct {
	mu   sync.RWMutex
	data map[string]interface{}
}

// newWorldModel creates a WorldModel pre-seeded with OS-level facts.
func newWorldModel() *WorldModel {
	wm := &WorldModel{
		data: make(map[string]interface{}),
	}
	wm.seed()
	return wm
}

// seed pre-populates the world model with basic OS facts.
func (wm *WorldModel) seed() {
	hostname, _ := os.Hostname()

	wm.data["os.platform"] = runtime.GOOS
	wm.data["os.arch"] = runtime.GOARCH
	wm.data["os.hostname"] = hostname
	wm.data["os.num_cpu"] = runtime.NumCPU()
	wm.data["os.go_version"] = runtime.Version()
}

// Update sets a fact in the world model.
func (wm *WorldModel) Update(key string, value interface{}) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.data[key] = value
}

// Lookup retrieves a fact from the world model.
// Returns the value and a boolean indicating whether the key was found.
func (wm *WorldModel) Lookup(key string) (interface{}, bool) {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	v, ok := wm.data[key]
	return v, ok
}

// Snapshot returns a shallow copy of the entire world model state.
func (wm *WorldModel) Snapshot() map[string]interface{} {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	snap := make(map[string]interface{}, len(wm.data))
	for k, v := range wm.data {
		snap[k] = v
	}
	return snap
}
