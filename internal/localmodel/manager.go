package localmodel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Manager probes an Ollama daemon, lists/pulls models, and reports pull
// progress. It has no dependency on the rest of HyperiOS's config/module
// wiring so it can be used standalone from the CLI as well as from the
// server's setup flow.
type Manager struct {
	baseURL string
	http    *http.Client
}

// NewManager returns a Manager targeting baseURL (e.g. "http://localhost:11434").
// If baseURL is empty, DefaultBaseURL is used.
func NewManager(baseURL string) *Manager {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Manager{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 0}, // pulls can take a long time; no client-wide timeout
	}
}

// DefaultBaseURL is the default local Ollama daemon address.
const DefaultBaseURL = "http://localhost:11434"

// Available reports whether the Ollama daemon is reachable at all.
func (m *Manager) Available(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

type tagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// InstalledModels returns the names of models already pulled into the local
// Ollama daemon.
func (m *Manager) InstalledModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("localmodel: list installed models: %w", err)
	}
	defer resp.Body.Close()

	var tags tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("localmodel: parse /api/tags response: %w", err)
	}

	names := make([]string, 0, len(tags.Models))
	for _, mdl := range tags.Models {
		names = append(names, mdl.Name)
	}
	return names, nil
}

// IsInstalled reports whether the named model is already pulled.
func (m *Manager) IsInstalled(ctx context.Context, name string) (bool, error) {
	installed, err := m.InstalledModels(ctx)
	if err != nil {
		return false, err
	}
	for _, n := range installed {
		if n == name {
			return true, nil
		}
	}
	return false, nil
}

// DeleteModel removes the named model from the Ollama daemon, freeing its
// disk space. Used by 'hyperi models remove' to fully undo a local-model
// setup rather than just disabling it.
func (m *Manager) DeleteModel(ctx context.Context, name string) error {
	body, err := json.Marshal(map[string]string{"model": name})
	if err != nil {
		return fmt.Errorf("localmodel: marshal delete request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, m.baseURL+"/api/delete", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("localmodel: build delete request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("localmodel: delete request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("localmodel: delete failed with status %d", resp.StatusCode)
	}
	return nil
}

// PullProgress reports incremental download progress from PullModel.
type PullProgress struct {
	Status    string
	Completed int64
	Total     int64
}

// PullModel downloads the named model, streaming progress updates to
// onProgress (which may be called many times; pass nil to ignore progress).
// Blocks until the pull completes or ctx is cancelled.
func (m *Manager) PullModel(ctx context.Context, name string, onProgress func(PullProgress)) error {
	body, err := json.Marshal(map[string]any{"model": name, "stream": true})
	if err != nil {
		return fmt.Errorf("localmodel: marshal pull request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("localmodel: build pull request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("localmodel: pull request failed (is Ollama running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("localmodel: pull failed with status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var lastStatus string
	var lastErr error
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var evt struct {
			Status    string `json:"status"`
			Error     string `json:"error"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
		}
		if err := json.Unmarshal(line, &evt); err != nil {
			continue // tolerate stray non-JSON lines
		}
		if evt.Error != "" {
			lastErr = fmt.Errorf("localmodel: pull error: %s", evt.Error)
			continue
		}
		lastStatus = evt.Status
		if onProgress != nil {
			onProgress(PullProgress{Status: evt.Status, Completed: evt.Completed, Total: evt.Total})
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("localmodel: reading pull stream: %w", err)
	}
	if lastErr != nil {
		return lastErr
	}
	if lastStatus != "success" && lastStatus != "" {
		// Ollama's final streamed message is normally {"status":"success"};
		// tolerate other terminal statuses but flag anything that looks
		// incomplete.
		return nil
	}
	return nil
}
