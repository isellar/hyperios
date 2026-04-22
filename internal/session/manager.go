package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Manager struct {
	sessionsDir string
}

func NewManager(sessionsDir string) *Manager {
	if sessionsDir == "" {
		home, _ := os.UserHomeDir()
		sessionsDir = filepath.Join(home, ".hyperi", "sessions")
	}
	return &Manager{sessionsDir: sessionsDir}
}

func (m *Manager) Save(state *State) error {
	if err := os.MkdirAll(m.sessionsDir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(m.sessionsDir, state.ID+".json")
	return os.WriteFile(path, data, 0600)
}

func (m *Manager) Load(id string) (*State, error) {
	path := filepath.Join(m.sessionsDir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}

	return &state, nil
}

func (m *Manager) List() ([]*State, error) {
	entries, err := os.ReadDir(m.sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*State{}, nil
		}
		return nil, err
	}

	var sessions []*State
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".json")
		state, err := m.Load(id)
		if err != nil {
			continue
		}
		sessions = append(sessions, state)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

func (m *Manager) Delete(id string) error {
	path := filepath.Join(m.sessionsDir, id+".json")
	return os.Remove(path)
}

func (m *Manager) Exists(id string) bool {
	path := filepath.Join(m.sessionsDir, id+".json")
	_, err := os.Stat(path)
	return err == nil
}

func (m *Manager) SessionsDir() string {
	return m.sessionsDir
}

func (m *Manager) CleanupOld(maxAge time.Duration) error {
	sessions, err := m.List()
	if err != nil {
		return err
	}

	cutoff := time.Now().Add(-maxAge)
	for _, s := range sessions {
		if s.UpdatedAt.Before(cutoff) {
			m.Delete(s.ID)
		}
	}

	return nil
}
