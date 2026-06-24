package io_toolbox_test

import (
	"strings"
	"testing"

	"github.com/isellar/hyperios/internal/io_toolbox"
	"github.com/isellar/hyperios/internal/io_toolbox/tools"
)

// ── ToolRegistry tests ────────────────────────────────────────────────────────

func TestToolRegistry_RegisterAndGet(t *testing.T) {
	reg := io_toolbox.NewToolRegistry()

	tool := tools.NewShellTool()
	reg.Register(tool)

	got, ok := reg.Get("shell")
	if !ok {
		t.Fatal("expected to find shell tool, got false")
	}
	if got.Name() != "shell" {
		t.Errorf("expected name %q, got %q", "shell", got.Name())
	}
}

func TestToolRegistry_GetMissing(t *testing.T) {
	reg := io_toolbox.NewToolRegistry()

	_, ok := reg.Get("nonexistent")
	if ok {
		t.Fatal("expected false for missing tool, got true")
	}
}

func TestToolRegistry_List(t *testing.T) {
	reg := io_toolbox.NewToolRegistry()
	reg.Register(tools.NewShellTool())
	reg.Register(tools.NewNotifyTool())

	names := reg.List()
	if len(names) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(names))
	}
	// List should be sorted
	if names[0] != "notify" || names[1] != "shell" {
		t.Errorf("unexpected order: %v", names)
	}
}

func TestToolRegistry_ListEmpty(t *testing.T) {
	reg := io_toolbox.NewToolRegistry()
	names := reg.List()
	if len(names) != 0 {
		t.Errorf("expected empty list, got %v", names)
	}
}

func TestToolRegistry_RegisterReplace(t *testing.T) {
	reg := io_toolbox.NewToolRegistry()
	reg.Register(tools.NewShellTool())
	reg.Register(tools.NewShellTool()) // replace same name

	names := reg.List()
	if len(names) != 1 {
		t.Errorf("expected 1 tool after replace, got %d", len(names))
	}
}

func TestToolRegistry_Execute_UnknownTool(t *testing.T) {
	reg := io_toolbox.NewToolRegistry()

	_, err := reg.Execute("ghost", "input")
	if err == nil {
		t.Fatal("expected error for unknown tool, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ── IOToolbox tests ───────────────────────────────────────────────────────────

func TestIOToolbox_ListTools(t *testing.T) {
	tb := io_toolbox.NewIOToolbox(nil)

	names := tb.ListTools()
	if len(names) < 3 {
		t.Fatalf("expected at least 3 built-in tools, got %d: %v", len(names), names)
	}

	want := map[string]bool{"shell": false, "notify": false, "schedule": false}
	for _, n := range names {
		want[n] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected tool %q to be registered", name)
		}
	}
}

func TestIOToolbox_GetTool(t *testing.T) {
	tb := io_toolbox.NewIOToolbox(nil)

	tool, ok := tb.GetTool("notify")
	if !ok {
		t.Fatal("expected notify tool to be registered")
	}
	if tool.Name() != "notify" {
		t.Errorf("expected name %q, got %q", "notify", tool.Name())
	}
}

func TestIOToolbox_ExecuteTool_UnknownTool(t *testing.T) {
	tb := io_toolbox.NewIOToolbox(nil)

	_, err := tb.ExecuteTool("does_not_exist", "")
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestIOToolbox_Health(t *testing.T) {
	tb := io_toolbox.NewIOToolbox(nil)

	h := tb.Health()
	if h.Status != "healthy" {
		t.Errorf("expected healthy, got %q: %s", h.Status, h.Details)
	}
}

func TestIOToolbox_Capabilities(t *testing.T) {
	tb := io_toolbox.NewIOToolbox(nil)

	caps := tb.Capabilities()
	if len(caps) == 0 {
		t.Fatal("expected at least one capability")
	}
}

func TestIOToolbox_Name(t *testing.T) {
	tb := io_toolbox.NewIOToolbox(nil)
	if tb.Name() != "io_toolbox" {
		t.Errorf("expected name %q, got %q", "io_toolbox", tb.Name())
	}
}

// ── NotifyTool tests ──────────────────────────────────────────────────────────

func TestNotifyTool_DoesNotPanic(t *testing.T) {
	nt := tools.NewNotifyTool()

	// Must not panic on any platform (Linux or not).
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NotifyTool.Execute panicked: %v", r)
		}
	}()

	_, _ = nt.Execute("test notification — should not panic")
}

func TestNotifyTool_Name(t *testing.T) {
	nt := tools.NewNotifyTool()
	if nt.Name() != "notify" {
		t.Errorf("expected %q, got %q", "notify", nt.Name())
	}
}

func TestNotifyTool_Description(t *testing.T) {
	nt := tools.NewNotifyTool()
	if nt.Description() == "" {
		t.Error("expected non-empty description")
	}
}

// ── ShellTool tests ───────────────────────────────────────────────────────────

func TestShellTool_Name(t *testing.T) {
	st := tools.NewShellTool()
	if st.Name() != "shell" {
		t.Errorf("expected %q, got %q", "shell", st.Name())
	}
}

func TestShellTool_Description(t *testing.T) {
	st := tools.NewShellTool()
	if st.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestShellTool_EmptyInput(t *testing.T) {
	st := tools.NewShellTool()
	out, err := st.Execute("")
	if err != nil {
		t.Errorf("unexpected error for empty input: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty output for empty input, got %q", out)
	}
}

// ── ScheduleTool tests ────────────────────────────────────────────────────────

func TestScheduleTool_Name(t *testing.T) {
	st := tools.NewScheduleTool(nil)
	if st.Name() != "schedule" {
		t.Errorf("expected %q, got %q", "schedule", st.Name())
	}
}

func TestScheduleTool_Description(t *testing.T) {
	st := tools.NewScheduleTool(nil)
	if st.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestScheduleTool_InvalidInput(t *testing.T) {
	st := tools.NewScheduleTool(nil)

	_, err := st.Execute("no-pipe-here")
	if err == nil {
		t.Fatal("expected error for missing pipe delimiter")
	}
}

func TestScheduleTool_EmptyCron(t *testing.T) {
	st := tools.NewScheduleTool(nil)

	_, err := st.Execute("|echo hello")
	if err == nil {
		t.Fatal("expected error for empty cron expression")
	}
}

func TestScheduleTool_EmptyCommand(t *testing.T) {
	st := tools.NewScheduleTool(nil)

	_, err := st.Execute("* * * * *|")
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestScheduleTool_ValidSchedule(t *testing.T) {
	st := tools.NewScheduleTool(nil)

	out, err := st.Execute("@every 1h|echo scheduled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty confirmation output")
	}
}
