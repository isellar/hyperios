package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MemoryEntry is a single record in long-term storage.
type MemoryEntry struct {
	Key          string      `json:"key"`
	Value        interface{} `json:"value"`
	Tags         []string    `json:"tags"`
	StoredAt     time.Time   `json:"stored_at"`
	LastAccessed time.Time   `json:"last_accessed"`
}

// LongTermMemory provides disk-backed key/value storage for the agent.
// Each entry is persisted as an individual JSON file under storagePath.
// The directory is created lazily on first write.
type LongTermMemory struct {
	mu          sync.RWMutex
	storagePath string
}

// newLongTermMemory creates a LongTermMemory backed by storagePath.
// The directory is created when the first entry is stored.
func newLongTermMemory(storagePath string) *LongTermMemory {
	return &LongTermMemory{storagePath: storagePath}
}

// entryPath returns the filesystem path for a given key.
// Keys are sanitised so they are safe as filenames.
func (lt *LongTermMemory) entryPath(key string) string {
	safe := sanitiseKey(key)
	return filepath.Join(lt.storagePath, safe+".json")
}

// sanitiseKey replaces characters that are unsafe in filenames with underscores.
func sanitiseKey(key string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
		" ", "_",
	)
	return replacer.Replace(key)
}

// Store persists value under key with optional tags.
// If an entry already exists for the key, it is overwritten.
func (lt *LongTermMemory) Store(key string, value interface{}, tags []string) error {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	if err := os.MkdirAll(lt.storagePath, 0o750); err != nil {
		return fmt.Errorf("memory: create storage dir: %w", err)
	}

	now := time.Now()
	entry := &MemoryEntry{
		Key:          key,
		Value:        value,
		Tags:         tags,
		StoredAt:     now,
		LastAccessed: now,
	}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("memory: marshal entry %q: %w", key, err)
	}

	tmp := lt.entryPath(key) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return fmt.Errorf("memory: write entry %q: %w", key, err)
	}
	if err := os.Rename(tmp, lt.entryPath(key)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("memory: rename entry %q: %w", key, err)
	}

	return nil
}

// Recall loads the MemoryEntry for key from disk.
// It updates LastAccessed on a successful read.
func (lt *LongTermMemory) Recall(key string) (*MemoryEntry, error) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	path := lt.entryPath(key)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("memory: key %q not found", key)
		}
		return nil, fmt.Errorf("memory: read entry %q: %w", key, err)
	}

	var entry MemoryEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("memory: parse entry %q: %w", key, err)
	}

	// Update LastAccessed in-place (best-effort; ignore write errors)
	entry.LastAccessed = time.Now()
	if updated, merr := json.MarshalIndent(entry, "", "  "); merr == nil {
		tmp := path + ".tmp"
		if werr := os.WriteFile(tmp, updated, 0o640); werr == nil {
			_ = os.Rename(tmp, path)
		}
	}

	return &entry, nil
}

// Search performs a simple case-insensitive substring match across all stored
// keys, values (JSON-serialised), and tags.
// Returns all entries where query appears in any of those fields.
func (lt *LongTermMemory) Search(query string) ([]*MemoryEntry, error) {
	lt.mu.RLock()
	defer lt.mu.RUnlock()

	entries, err := lt.loadAll()
	if err != nil {
		return nil, err
	}

	q := strings.ToLower(query)
	var results []*MemoryEntry
	for _, e := range entries {
		if lt.matches(e, q) {
			results = append(results, e)
		}
	}
	return results, nil
}

// matches reports whether the entry matches the lowercased query string.
func (lt *LongTermMemory) matches(e *MemoryEntry, q string) bool {
	if strings.Contains(strings.ToLower(e.Key), q) {
		return true
	}
	// Serialise value to JSON for substring matching
	if vb, err := json.Marshal(e.Value); err == nil {
		if strings.Contains(strings.ToLower(string(vb)), q) {
			return true
		}
	}
	for _, tag := range e.Tags {
		if strings.Contains(strings.ToLower(tag), q) {
			return true
		}
	}
	return false
}

// Forget removes the stored entry for key from disk.
// Returns an error if the key does not exist.
func (lt *LongTermMemory) Forget(key string) error {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	path := lt.entryPath(key)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("memory: key %q not found", key)
		}
		return fmt.Errorf("memory: forget %q: %w", key, err)
	}
	return nil
}

// loadAll reads every JSON file in storagePath and returns parsed entries.
// The caller is responsible for holding the appropriate lock.
func (lt *LongTermMemory) loadAll() ([]*MemoryEntry, error) {
	entries, err := os.ReadDir(lt.storagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("memory: list storage dir: %w", err)
	}

	var results []*MemoryEntry
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		// Skip temp files
		if strings.HasSuffix(de.Name(), ".tmp") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(lt.storagePath, de.Name()))
		if err != nil {
			continue
		}
		var entry MemoryEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			continue
		}
		results = append(results, &entry)
	}
	return results, nil
}
