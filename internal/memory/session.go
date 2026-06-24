// Package memory provides session, world-model, and long-term storage for the HyperiOS agent.
package memory

import "sync"

// SessionMemory holds ephemeral key/value context for the current session.
// Data is never persisted to disk; it lives only for the lifetime of the process.
type SessionMemory struct {
	mu   sync.RWMutex
	data map[string]interface{}
}

// newSessionMemory returns an empty SessionMemory.
func newSessionMemory() *SessionMemory {
	return &SessionMemory{
		data: make(map[string]interface{}),
	}
}

// StoreSession saves value under key for this session.
func (s *SessionMemory) StoreSession(key string, value interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return nil
}

// RecallSession returns the value stored under key and whether it was found.
func (s *SessionMemory) RecallSession(key string) (interface{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

// ClearSession removes all keys from this session's memory.
func (s *SessionMemory) ClearSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = make(map[string]interface{})
}
