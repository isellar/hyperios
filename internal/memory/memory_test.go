package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isellar/hyperios/internal/config"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hyperi-memory-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func newTestMemory(t *testing.T) *Memory {
	t.Helper()
	cfg := config.Defaults()
	cfg.MemoryStoragePath = filepath.Join(tempDir(t), "memory")
	return NewMemory(cfg)
}

// ---------------------------------------------------------------------------
// SessionMemory tests
// ---------------------------------------------------------------------------

func TestSessionMemory_StoreAndRecall(t *testing.T) {
	sm := newSessionMemory()

	if err := sm.StoreSession("greeting", "hello"); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	v, ok := sm.RecallSession("greeting")
	if !ok {
		t.Fatal("RecallSession: key not found")
	}
	if v != "hello" {
		t.Fatalf("RecallSession: want %q, got %v", "hello", v)
	}
}

func TestSessionMemory_MissingKey(t *testing.T) {
	sm := newSessionMemory()
	_, ok := sm.RecallSession("nonexistent")
	if ok {
		t.Fatal("RecallSession: expected false for missing key")
	}
}

func TestSessionMemory_Clear(t *testing.T) {
	sm := newSessionMemory()
	_ = sm.StoreSession("k", "v")
	sm.ClearSession()

	_, ok := sm.RecallSession("k")
	if ok {
		t.Fatal("ClearSession: key still present after clear")
	}
}

func TestSessionMemory_Overwrite(t *testing.T) {
	sm := newSessionMemory()
	_ = sm.StoreSession("k", "first")
	_ = sm.StoreSession("k", "second")

	v, ok := sm.RecallSession("k")
	if !ok {
		t.Fatal("RecallSession: key not found after overwrite")
	}
	if v != "second" {
		t.Fatalf("RecallSession after overwrite: want %q, got %v", "second", v)
	}
}

// ---------------------------------------------------------------------------
// WorldModel tests
// ---------------------------------------------------------------------------

func TestWorldModel_Seed(t *testing.T) {
	wm := newWorldModel()

	for _, key := range []string{"os.platform", "os.arch", "os.hostname", "os.num_cpu", "os.go_version"} {
		v, ok := wm.Lookup(key)
		if !ok {
			t.Errorf("WorldModel seed: missing key %q", key)
		}
		if v == nil {
			t.Errorf("WorldModel seed: nil value for key %q", key)
		}
	}
}

func TestWorldModel_UpdateAndLookup(t *testing.T) {
	wm := newWorldModel()
	wm.Update("network.status", "connected")

	v, ok := wm.Lookup("network.status")
	if !ok {
		t.Fatal("WorldModel Lookup: key not found after Update")
	}
	if v != "connected" {
		t.Fatalf("WorldModel Lookup: want %q, got %v", "connected", v)
	}
}

func TestWorldModel_LookupMissing(t *testing.T) {
	wm := newWorldModel()
	_, ok := wm.Lookup("no.such.key")
	if ok {
		t.Fatal("WorldModel Lookup: expected false for missing key")
	}
}

func TestWorldModel_Snapshot(t *testing.T) {
	wm := newWorldModel()
	wm.Update("foo", "bar")

	snap := wm.Snapshot()
	if _, ok := snap["foo"]; !ok {
		t.Fatal("Snapshot: missing key foo")
	}
	if _, ok := snap["os.platform"]; !ok {
		t.Fatal("Snapshot: missing seeded key os.platform")
	}

	// Mutations to the snapshot must not affect the world model
	snap["foo"] = "mutated"
	v, _ := wm.Lookup("foo")
	if v == "mutated" {
		t.Fatal("Snapshot: mutation affected world model (not a copy)")
	}
}

// ---------------------------------------------------------------------------
// LongTermMemory tests
// ---------------------------------------------------------------------------

func TestLongTermMemory_StoreAndRecall(t *testing.T) {
	lt := newLongTermMemory(filepath.Join(tempDir(t), "lt"))

	if err := lt.Store("project", "hyperios", []string{"work"}); err != nil {
		t.Fatalf("Store: %v", err)
	}

	entry, err := lt.Recall("project")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if entry.Key != "project" {
		t.Fatalf("Recall: want key %q, got %q", "project", entry.Key)
	}
	if entry.Value != "hyperios" {
		t.Fatalf("Recall: want value %q, got %v", "hyperios", entry.Value)
	}
	if len(entry.Tags) != 1 || entry.Tags[0] != "work" {
		t.Fatalf("Recall: want tags [work], got %v", entry.Tags)
	}
}

func TestLongTermMemory_RecallMissing(t *testing.T) {
	lt := newLongTermMemory(filepath.Join(tempDir(t), "lt"))
	_, err := lt.Recall("ghost")
	if err == nil {
		t.Fatal("Recall: expected error for missing key")
	}
}

func TestLongTermMemory_Forget(t *testing.T) {
	lt := newLongTermMemory(filepath.Join(tempDir(t), "lt"))
	_ = lt.Store("temp", "data", nil)

	if err := lt.Forget("temp"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	_, err := lt.Recall("temp")
	if err == nil {
		t.Fatal("Recall after Forget: expected error, got nil")
	}
}

func TestLongTermMemory_ForgetMissing(t *testing.T) {
	lt := newLongTermMemory(filepath.Join(tempDir(t), "lt"))
	err := lt.Forget("nonexistent")
	if err == nil {
		t.Fatal("Forget: expected error for missing key")
	}
}

func TestLongTermMemory_Search(t *testing.T) {
	lt := newLongTermMemory(filepath.Join(tempDir(t), "lt"))

	entries := []struct {
		key   string
		value string
		tags  []string
	}{
		{"nginx.config", "server { listen 80; }", []string{"web", "config"}},
		{"ssh.key", "ed25519-...", []string{"security"}},
		{"project.name", "hyperios", []string{"meta"}},
	}

	for _, e := range entries {
		if err := lt.Store(e.key, e.value, e.tags); err != nil {
			t.Fatalf("Store %q: %v", e.key, err)
		}
	}

	tests := []struct {
		query    string
		wantKeys []string
	}{
		{"nginx", []string{"nginx.config"}},
		{"security", []string{"ssh.key"}},        // tag match
		{"hyperios", []string{"project.name"}},   // value match
		{"config", []string{"nginx.config"}},     // key + tag match (deduped)
		{"NGINX", []string{"nginx.config"}},      // case insensitive
		{"zzz-nomatch", []string{}},
	}

	for _, tc := range tests {
		results, err := lt.Search(tc.query)
		if err != nil {
			t.Fatalf("Search(%q): %v", tc.query, err)
		}

		got := make(map[string]bool)
		for _, r := range results {
			got[r.Key] = true
		}
		for _, wk := range tc.wantKeys {
			if !got[wk] {
				t.Errorf("Search(%q): missing expected key %q in results %v", tc.query, wk, results)
			}
		}
		if tc.query == "zzz-nomatch" && len(results) != 0 {
			t.Errorf("Search(%q): expected no results, got %d", tc.query, len(results))
		}
	}
}

func TestLongTermMemory_SearchEmpty(t *testing.T) {
	lt := newLongTermMemory(filepath.Join(tempDir(t), "lt"))
	_ = lt.Store("a", "alpha", nil)
	_ = lt.Store("b", "beta", nil)

	// Empty query should match all entries
	results, err := lt.Search("")
	if err != nil {
		t.Fatalf("Search empty: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Search empty: want 2 results, got %d", len(results))
	}
}

func TestLongTermMemory_Overwrite(t *testing.T) {
	lt := newLongTermMemory(filepath.Join(tempDir(t), "lt"))
	_ = lt.Store("k", "v1", nil)
	_ = lt.Store("k", "v2", []string{"updated"})

	entry, err := lt.Recall("k")
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if entry.Value != "v2" {
		t.Fatalf("Recall after overwrite: want %q, got %v", "v2", entry.Value)
	}
}

func TestLongTermMemory_KeySanitisation(t *testing.T) {
	lt := newLongTermMemory(filepath.Join(tempDir(t), "lt"))
	key := "path/to/some:value"
	_ = lt.Store(key, "test", nil)

	// Verify the file was created with a sanitised name (no slashes)
	safe := sanitiseKey(key)
	path := filepath.Join(lt.storagePath, safe+".json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sanitised file not found at %s: %v", path, err)
	}

	// Recall must still work via the original key
	entry, err := lt.Recall(key)
	if err != nil {
		t.Fatalf("Recall with sanitised key: %v", err)
	}
	if entry.Value != "test" {
		t.Fatalf("Recall: want %q, got %v", "test", entry.Value)
	}
}

// ---------------------------------------------------------------------------
// Memory (top-level) tests
// ---------------------------------------------------------------------------

func TestMemory_StoreAndRecallContext(t *testing.T) {
	m := newTestMemory(t)

	if err := m.StoreContext("last.command", "ls -la"); err != nil {
		t.Fatalf("StoreContext: %v", err)
	}

	v, ok := m.RecallContext("last.command")
	if !ok {
		t.Fatal("RecallContext: key not found")
	}
	if v != "ls -la" {
		t.Fatalf("RecallContext: want %q, got %v", "ls -la", v)
	}
}

func TestMemory_RecallFromLongTerm(t *testing.T) {
	m := newTestMemory(t)

	// Write directly to long-term, bypassing session
	if err := m.longTerm.Store("direct.key", "direct.value", nil); err != nil {
		t.Fatalf("long-term Store: %v", err)
	}

	v, ok := m.RecallContext("direct.key")
	if !ok {
		t.Fatal("RecallContext: key not found in long-term")
	}
	if v != "direct.value" {
		t.Fatalf("RecallContext: want %q, got %v", "direct.value", v)
	}

	// Second recall should hit session cache
	v2, ok2 := m.RecallContext("direct.key")
	if !ok2 || v2 != "direct.value" {
		t.Fatal("RecallContext (cached): unexpected result")
	}
}

func TestMemory_RecallMissing(t *testing.T) {
	m := newTestMemory(t)
	_, ok := m.RecallContext("no.such.key")
	if ok {
		t.Fatal("RecallContext: expected false for missing key")
	}
}

func TestMemory_SearchContext(t *testing.T) {
	m := newTestMemory(t)
	_ = m.StoreContext("tool.grep", "grep binary path: /usr/bin/grep")
	_ = m.StoreContext("tool.curl", "curl binary path: /usr/bin/curl")

	results, err := m.SearchContext("grep")
	if err != nil {
		t.Fatalf("SearchContext: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("SearchContext: want 1 result, got %d", len(results))
	}
	if results[0].Key != "tool.grep" {
		t.Fatalf("SearchContext: unexpected key %q", results[0].Key)
	}
}

func TestMemory_GetWorldModel(t *testing.T) {
	m := newTestMemory(t)
	wm := m.GetWorldModel()
	if wm == nil {
		t.Fatal("GetWorldModel: returned nil")
	}
	_, ok := wm.Lookup("os.platform")
	if !ok {
		t.Fatal("GetWorldModel: missing seeded key os.platform")
	}
}

// ---------------------------------------------------------------------------
// MemoryProvider interface tests (goal_fulfillment compatibility)
// ---------------------------------------------------------------------------

func TestMemory_GetContext(t *testing.T) {
	m := newTestMemory(t)
	_ = m.StoreContext("intent.last", "open a terminal")

	s, err := m.GetContext("intent.last")
	if err != nil {
		t.Fatalf("GetContext: %v", err)
	}
	if s != "open a terminal" {
		t.Fatalf("GetContext: want %q, got %q", "open a terminal", s)
	}
}

func TestMemory_GetContextMissing(t *testing.T) {
	m := newTestMemory(t)
	_, err := m.GetContext("missing.key")
	if err == nil {
		t.Fatal("GetContext: expected error for missing key")
	}
}

func TestMemory_GetContextNonString(t *testing.T) {
	m := newTestMemory(t)
	_ = m.StoreContext("count", 42)

	s, err := m.GetContext("count")
	if err != nil {
		t.Fatalf("GetContext: %v", err)
	}
	if !strings.Contains(s, "42") {
		t.Fatalf("GetContext: expected %q to contain '42'", s)
	}
}

// ---------------------------------------------------------------------------
// module.Module interface tests
// ---------------------------------------------------------------------------

func TestMemory_Name(t *testing.T) {
	m := newTestMemory(t)
	if m.Name() != "memory" {
		t.Fatalf("Name: want %q, got %q", "memory", m.Name())
	}
}

func TestMemory_Health(t *testing.T) {
	m := newTestMemory(t)
	h := m.Health()
	if h.Status != "healthy" {
		t.Fatalf("Health: want healthy, got %q (%s)", h.Status, h.Details)
	}
	if h.Timestamp.IsZero() {
		t.Fatal("Health: Timestamp is zero")
	}
}

func TestMemory_Capabilities(t *testing.T) {
	m := newTestMemory(t)
	caps := m.Capabilities()
	if len(caps) == 0 {
		t.Fatal("Capabilities: returned empty slice")
	}
}
