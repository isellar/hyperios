package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
//
// Search relevance is served by an in-memory inverted index (index.go)
// rather than rescanning every file on disk for every call. The index is
// built lazily on first use (loaded once from whatever is already on disk)
// and then kept incrementally in sync by Store/Forget — no full rescan is
// needed again unless the process restarts.
type LongTermMemory struct {
	mu          sync.Mutex
	storagePath string
	idx         *invertedIndex
	loaded      bool
	// entries caches the fully-parsed entry for every indexed key, keyed by
	// the original (unsanitised) key, so Search can resolve index hits back
	// to *MemoryEntry without a per-entry disk read.
	entries map[string]*MemoryEntry
}

// newLongTermMemory creates a LongTermMemory backed by storagePath.
// The directory is created when the first entry is stored.
func newLongTermMemory(storagePath string) *LongTermMemory {
	return &LongTermMemory{
		storagePath: storagePath,
		idx:         newInvertedIndex(),
		entries:     make(map[string]*MemoryEntry),
	}
}

// ensureLoadedLocked populates the in-memory index and entry cache from
// disk on first use. The caller must hold lt.mu. Subsequent calls are a
// no-op (the index is kept in sync incrementally by Store/Forget from then
// on, so there's no need to ever rescan the whole directory again within
// the same process lifetime).
func (lt *LongTermMemory) ensureLoadedLocked() error {
	if lt.loaded {
		return nil
	}
	all, err := lt.loadAll()
	if err != nil {
		return err
	}
	for _, e := range all {
		lt.entries[e.Key] = e
		lt.idx.add(e.Key, tokenize(documentText(e)))
	}
	lt.loaded = true
	return nil
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

	// Load whatever's already on disk before applying this write, so the
	// in-memory index reflects the full corpus rather than just entries
	// written since process start.
	if err := lt.ensureLoadedLocked(); err != nil {
		return err
	}

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

	// Keep the in-memory index/cache in sync with what's now on disk,
	// rather than requiring a reload on the next Search call.
	lt.entries[key] = entry
	lt.idx.update(key, tokenize(documentText(entry)))

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

	// Keep the cache consistent if this key is (or becomes) tracked. Token
	// content (key/value/tags) is unchanged by a recall, so the index
	// itself doesn't need updating — only the cached entry's LastAccessed.
	if lt.loaded {
		lt.entries[key] = &entry
	}

	return &entry, nil
}

// Search returns entries relevant to query, ranked by BM25 relevance score
// (highest first) rather than the arbitrary filesystem-listing order of the
// previous substring-based implementation. query is tokenized into words
// (see tokenize); an entry's score depends on how many query terms it
// contains, weighted by how distinctive each term is across the whole
// corpus (rarer terms count for more) and normalized for document length,
// so a long entry that happens to mention a common word once doesn't
// outrank a short, highly relevant one.
//
// An empty query is treated as "return every entry" (used e.g. by
// Memory.Report to count total entries), with no ranking applied since
// there's nothing to rank against.
func (lt *LongTermMemory) Search(query string) ([]*MemoryEntry, error) {
	return lt.SearchTopN(query, 0)
}

// SearchTopN is like Search but returns at most limit results (the
// highest-scoring ones). limit <= 0 means unbounded, matching Search.
func (lt *LongTermMemory) SearchTopN(query string, limit int) ([]*MemoryEntry, error) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	if err := lt.ensureLoadedLocked(); err != nil {
		return nil, err
	}

	if strings.TrimSpace(query) == "" {
		results := make([]*MemoryEntry, 0, len(lt.entries))
		for _, e := range lt.entries {
			results = append(results, e)
		}
		sort.Slice(results, func(i, j int) bool { return results[i].Key < results[j].Key })
		if limit > 0 && len(results) > limit {
			results = results[:limit]
		}
		return results, nil
	}

	tokens := tokenize(query)
	if len(tokens) == 0 {
		return nil, nil
	}

	scored := lt.idx.score(tokens)
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		// Stable tie-break so results are deterministic across calls.
		return scored[i].key < scored[j].key
	})
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}

	results := make([]*MemoryEntry, 0, len(scored))
	for _, sd := range scored {
		if e, ok := lt.entries[sd.key]; ok {
			results = append(results, e)
		}
	}
	return results, nil
}

// SearchByTag returns every entry that has an exact (case-insensitive) match
// for tag among its Tags. Unlike Search, this does not rank by relevance —
// it won't accidentally pull in unrelated entries whose content happens to
// contain the tag as a word, since it checks the Tags field only — used for
// directive lookup, where we need "give me exactly the directive-tagged
// entries" rather than a fuzzy search.
func (lt *LongTermMemory) SearchByTag(tag string) ([]*MemoryEntry, error) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	if err := lt.ensureLoadedLocked(); err != nil {
		return nil, err
	}

	tagLower := strings.ToLower(tag)
	var results []*MemoryEntry
	for _, e := range lt.entries {
		for _, t := range e.Tags {
			if strings.ToLower(t) == tagLower {
				results = append(results, e)
				break
			}
		}
	}
	// Deterministic ordering for callers/tests.
	sort.Slice(results, func(i, j int) bool { return results[i].Key < results[j].Key })
	return results, nil
}

// Forget removes the stored entry for key from disk.
// Returns an error if the key does not exist.
func (lt *LongTermMemory) Forget(key string) error {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	if err := lt.ensureLoadedLocked(); err != nil {
		return err
	}

	path := lt.entryPath(key)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("memory: key %q not found", key)
		}
		return fmt.Errorf("memory: forget %q: %w", key, err)
	}

	lt.idx.remove(key)
	delete(lt.entries, key)

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
