// Package io_toolbox provides a toolbox of I/O tools that agents can invoke.
// Each tool implements the Tool interface and is registered in a ToolRegistry.
// The IOToolbox wraps the registry and exposes it as a module.Module.
package io_toolbox

import (
	"fmt"
	"sort"
	"sync"
)

// Tool is the interface that all agent-accessible I/O tools must implement.
type Tool interface {
	// Name returns the unique identifier for this tool.
	Name() string
	// Description returns a human-readable description of what this tool does.
	Description() string
	// Execute runs the tool with the given input and returns its output.
	Execute(input string) (string, error)
}

// ToolRegistry is a thread-safe registry mapping tool names to Tool implementations.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewToolRegistry creates an empty ToolRegistry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry. If a tool with the same name is already
// registered, it is replaced.
func (r *ToolRegistry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name()] = tool
}

// Get retrieves a tool by name. Returns the tool and true if found, or nil and
// false if no tool with that name is registered.
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns the names of all registered tools in sorted order.
func (r *ToolRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Execute runs the named tool with the given input. Returns an error if the
// tool is not found.
func (r *ToolRegistry) Execute(name, input string) (string, error) {
	t, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("io_toolbox: tool %q not found", name)
	}
	return t.Execute(input)
}
