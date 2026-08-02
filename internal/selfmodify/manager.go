// Package selfmodify lets the running HyperiOS agent rebuild, verify, and
// swap in a modified version of its own binary — the mechanism behind
// "the agent can improve its own code, not just submit goals about it."
//
// Safety model: every Apply() re-runs the same gate CI enforces (go build,
// go vet, go test ./...) against the source tree before touching the running
// binary. A failing build/vet/test never gets applied — the manager reports
// exactly what failed so an agent (or a human) can fix it and retry. Every
// applied binary is backed up first, and Rollback() restores the most recent
// backup. Nothing here is used unless explicitly enabled — see
// cmd/hyperi/selfmodify_setup.go's 'hyperi selfmodify enable' flow.
package selfmodify

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// CheckResult holds the output of a single build/vet/test step.
type CheckResult struct {
	Name    string `json:"name"` // "build", "vet", or "test"
	Passed  bool   `json:"passed"`
	Output  string `json:"output"`
	Elapsed string `json:"elapsed"`
}

// VerifyResult is the outcome of running the full build+vet+test gate.
type VerifyResult struct {
	Passed bool          `json:"passed"`
	Steps  []CheckResult `json:"steps"`
}

// Summary returns a compact human/LLM-readable summary of the verify result.
func (v *VerifyResult) Summary() string {
	var sb strings.Builder
	if v.Passed {
		sb.WriteString("All checks passed:\n")
	} else {
		sb.WriteString("Checks FAILED:\n")
	}
	for _, s := range v.Steps {
		status := "PASS"
		if !s.Passed {
			status = "FAIL"
		}
		fmt.Fprintf(&sb, "  [%s] %s (%s)\n", status, s.Name, s.Elapsed)
		if !s.Passed {
			out := s.Output
			if len(out) > 4000 {
				out = out[len(out)-4000:] // keep the tail — that's where the actual error usually is
			}
			fmt.Fprintf(&sb, "    output:\n%s\n", indent(out, "    "))
		}
	}
	return sb.String()
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// Manager drives the rebuild/verify/apply/rollback lifecycle.
type Manager struct {
	// sourceDir is the root of the HyperiOS source tree (contains go.mod).
	sourceDir string
	// binaryPath is the currently-running binary's installed location, e.g.
	// /usr/local/bin/hyperi. Apply() replaces this file; the running process
	// then re-execs into it.
	binaryPath string
	// backupDir holds timestamped copies of previously-applied binaries.
	backupDir string
	// buildTimeout bounds each individual build/vet/test step.
	buildTimeout time.Duration
	// reExecDelay is how long Apply()/Rollback() wait after swapping the
	// binary in before calling syscall.Exec, giving the caller (the
	// tool-use loop) a chance to receive the tool result and let the agent
	// produce a final summary before the process image is replaced.
	reExecDelay time.Duration
	// reExecEnabled gates whether Apply()/Rollback() actually re-exec after
	// a successful swap. This must be true only for a Manager backing a
	// long-running `hyperi serve` process: re-exec replays os.Args, which
	// is correct for restarting a server (same command re-launches the
	// server) but wrong for a one-shot CLI invocation like `hyperi
	// selfmodify rollback` — replaying that argv would just re-run the same
	// one-shot rollback command again in the new process, indefinitely.
	// CLI commands construct their Manager with reExecEnabled=false and
	// print a "restart manually" message instead.
	reExecEnabled bool

	mu         sync.Mutex
	lastVerify *VerifyResult
}

// Options configures a Manager. Zero values fall back to sane defaults.
type Options struct {
	BuildTimeout time.Duration // default 5 minutes
	ReExecDelay  time.Duration // default 2 seconds
	// ReExecEnabled, if true, causes Apply()/Rollback() to re-exec the
	// process (replacing it with the newly-installed binary, same PID,
	// same argv) after a successful swap. Set this true only when the
	// Manager backs a long-running `hyperi serve` process — see the
	// reExecEnabled field doc for why one-shot CLI commands must leave
	// this false.
	ReExecEnabled bool
}

// NewManager returns a Manager for the source tree at sourceDir, targeting
// the installed binary at binaryPath. Re-exec after Apply/Rollback is
// disabled unless opts.ReExecEnabled is set — see Options.ReExecEnabled.
func NewManager(sourceDir, binaryPath string, opts Options) *Manager {
	if opts.BuildTimeout <= 0 {
		opts.BuildTimeout = 5 * time.Minute
	}
	if opts.ReExecDelay <= 0 {
		opts.ReExecDelay = 2 * time.Second
	}
	return &Manager{
		sourceDir:     sourceDir,
		binaryPath:    binaryPath,
		backupDir:     binaryPath + ".backups",
		buildTimeout:  opts.BuildTimeout,
		reExecDelay:   opts.ReExecDelay,
		reExecEnabled: opts.ReExecEnabled,
	}
}

// Verify runs `go build`, `go vet ./...`, and `go test ./...` against the
// source tree, in that order, stopping at the first failure. This mirrors
// the project's own CI gate (.github/workflows/ci.yml) so a modification
// that wouldn't pass CI is never applied to the running binary.
func (m *Manager) Verify(ctx context.Context) (*VerifyResult, error) {
	result := &VerifyResult{Passed: true}

	tmpBinary := filepath.Join(os.TempDir(), fmt.Sprintf("hyperi-verify-%d", time.Now().UnixNano()))
	defer os.Remove(tmpBinary)

	steps := []struct {
		name string
		args []string
	}{
		{"build", []string{"build", "-o", tmpBinary, "./cmd/hyperi"}},
		{"vet", []string{"vet", "./..."}},
		{"test", []string{"test", "./..."}},
	}

	for _, step := range steps {
		cr, err := m.runGo(ctx, step.name, step.args)
		result.Steps = append(result.Steps, cr)
		if err != nil {
			return result, err
		}
		if !cr.Passed {
			result.Passed = false
			m.mu.Lock()
			m.lastVerify = result
			m.mu.Unlock()
			return result, nil // not a Go error — a legitimate failed check
		}
	}

	m.mu.Lock()
	m.lastVerify = result
	m.mu.Unlock()
	return result, nil
}

// runGo executes `go <args...>` in sourceDir with a bounded timeout and
// captures combined output.
func (m *Manager) runGo(ctx context.Context, name string, args []string) (CheckResult, error) {
	ctx, cancel := context.WithTimeout(ctx, m.buildTimeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = m.sourceDir

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	elapsed := time.Since(start)

	cr := CheckResult{
		Name:    name,
		Passed:  err == nil,
		Output:  buf.String(),
		Elapsed: elapsed.Round(time.Millisecond).String(),
	}

	if err != nil && ctx.Err() == context.DeadlineExceeded {
		cr.Output += fmt.Sprintf("\n(timed out after %s)", m.buildTimeout)
		return cr, nil // timeout is a legitimate failure, not an infra error
	}
	if err != nil {
		// Any non-zero exit from `go build`/`go vet`/`go test` is a normal,
		// expected "this failed" outcome — not a Go/infra-level error.
		return cr, nil
	}
	return cr, nil
}

// LastVerify returns the most recent Verify() result, if any has run yet.
func (m *Manager) LastVerify() (*VerifyResult, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastVerify, m.lastVerify != nil
}

// Apply builds a fresh binary, runs the full verify gate, and — only if
// every check passes — backs up the currently-installed binary, swaps the
// new one into place, and schedules a re-exec (after reExecDelay) so the
// running process becomes the new binary at the same PID.
//
// Returns a description of what happened (including verify output on
// failure) and an error only for infra-level problems (e.g. can't write the
// backup). A failed verify is reported via the returned string, not an
// error — that's the expected "the proposed change doesn't pass CI" outcome,
// which the caller (typically an LLM agent) should be able to read and act
// on rather than treat as a crash.
func (m *Manager) Apply(ctx context.Context) (string, error) {
	result, err := m.Verify(ctx)
	if err != nil {
		return "", fmt.Errorf("selfmodify: verify: %w", err)
	}
	if !result.Passed {
		return "Apply refused: verification failed.\n\n" + result.Summary(), nil
	}

	backupPath, err := m.backupCurrentBinary()
	if err != nil {
		return "", fmt.Errorf("selfmodify: backup current binary: %w", err)
	}

	tmpBinary := filepath.Join(os.TempDir(), fmt.Sprintf("hyperi-apply-%d", time.Now().UnixNano()))
	buildCmd := exec.CommandContext(ctx, "go", "build", "-o", tmpBinary, "./cmd/hyperi")
	buildCmd.Dir = m.sourceDir
	var buildOut bytes.Buffer
	buildCmd.Stdout = &buildOut
	buildCmd.Stderr = &buildOut
	if err := buildCmd.Run(); err != nil {
		return "", fmt.Errorf("selfmodify: final build before swap failed unexpectedly after verify passed: %w\n%s", err, buildOut.String())
	}
	defer os.Remove(tmpBinary)

	if err := installBinary(tmpBinary, m.binaryPath); err != nil {
		return "", fmt.Errorf("selfmodify: install new binary: %w", err)
	}

	if !m.reExecEnabled {
		return fmt.Sprintf(
			"Verification passed. New binary installed at %s (previous version backed up to %s). "+
				"Not restarting automatically — restart the process manually to run the new version.",
			m.binaryPath, backupPath,
		), nil
	}

	go func() {
		time.Sleep(m.reExecDelay)
		reExec(m.binaryPath)
	}()

	return fmt.Sprintf(
		"Verification passed. New binary installed at %s (previous version backed up to %s). "+
			"Restarting into the new version in %s.",
		m.binaryPath, backupPath, m.reExecDelay,
	), nil
}

// installBinary atomically replaces dst with the contents of src, preserving
// dst's file mode (typically 0755) so the installed binary stays executable.
func installBinary(src, dst string) error {
	info, err := os.Stat(dst)
	mode := os.FileMode(0o755)
	if err == nil {
		mode = info.Mode()
	}

	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read built binary: %w", err)
	}

	tmp := dst + ".new"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("write staged binary: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}

// backupCurrentBinary copies the currently-installed binary into backupDir
// under a timestamped name, pruning old backups beyond maxBackups.
func (m *Manager) backupCurrentBinary() (string, error) {
	if err := os.MkdirAll(m.backupDir, 0o750); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}

	data, err := os.ReadFile(m.binaryPath)
	if err != nil {
		return "", fmt.Errorf("read current binary: %w", err)
	}

	name := fmt.Sprintf("hyperi-%d", time.Now().UnixNano())
	backupPath := filepath.Join(m.backupDir, name)
	if err := os.WriteFile(backupPath, data, 0o755); err != nil {
		return "", fmt.Errorf("write backup: %w", err)
	}

	m.pruneBackups(maxBackups)
	return backupPath, nil
}

// maxBackups bounds how many prior binaries are kept on disk.
const maxBackups = 5

// pruneBackups removes all but the keep most recent backups.
func (m *Manager) pruneBackups(keep int) {
	backups, err := m.listBackups()
	if err != nil || len(backups) <= keep {
		return
	}
	for _, b := range backups[:len(backups)-keep] {
		_ = os.Remove(filepath.Join(m.backupDir, b))
	}
}

// listBackups returns backup filenames sorted oldest-first (by the
// timestamp embedded in the filename).
func (m *Manager) listBackups() ([]string, error) {
	entries, err := os.ReadDir(m.backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "hyperi-") {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool {
		return backupTimestamp(names[i]) < backupTimestamp(names[j])
	})
	return names, nil
}

// backupTimestamp extracts the UnixNano timestamp embedded in a
// "hyperi-<ts>" backup filename, or 0 if it can't be parsed.
func backupTimestamp(name string) int64 {
	ts := strings.TrimPrefix(name, "hyperi-")
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// ListBackups returns the available backup binaries, newest first, along
// with the time each was created.
func (m *Manager) ListBackups() ([]BackupInfo, error) {
	names, err := m.listBackups()
	if err != nil {
		return nil, err
	}
	infos := make([]BackupInfo, 0, len(names))
	for i := len(names) - 1; i >= 0; i-- { // newest first
		ts := backupTimestamp(names[i])
		infos = append(infos, BackupInfo{
			Name:      names[i],
			CreatedAt: time.Unix(0, ts),
		})
	}
	return infos, nil
}

// BackupInfo describes one available rollback target.
type BackupInfo struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Rollback restores the most recently created backup binary and re-execs
// into it (after reExecDelay, same as Apply). Returns an error if no backup
// exists.
func (m *Manager) Rollback() (string, error) {
	backups, err := m.listBackups()
	if err != nil {
		return "", fmt.Errorf("selfmodify: list backups: %w", err)
	}
	if len(backups) == 0 {
		return "", fmt.Errorf("selfmodify: no backups available to roll back to")
	}

	latest := backups[len(backups)-1]
	backupPath := filepath.Join(m.backupDir, latest)

	if err := installBinary(backupPath, m.binaryPath); err != nil {
		return "", fmt.Errorf("selfmodify: restore backup: %w", err)
	}

	if !m.reExecEnabled {
		return fmt.Sprintf("Restored backup %s. Not restarting automatically — restart the process manually to run the restored version.", latest), nil
	}

	go func() {
		time.Sleep(m.reExecDelay)
		reExec(m.binaryPath)
	}()

	return fmt.Sprintf("Restored backup %s. Restarting in %s.", latest, m.reExecDelay), nil
}

// Status reports the manager's current configuration and state for display
// (e.g. `hyperi selfmodify status`, or the self_modify tool's "status" action).
type Status struct {
	SourceDir  string        `json:"source_dir"`
	BinaryPath string        `json:"binary_path"`
	Backups    []BackupInfo  `json:"backups"`
	LastVerify *VerifyResult `json:"last_verify,omitempty"`
}

// GetStatus returns the current Status.
func (m *Manager) GetStatus() Status {
	backups, _ := m.ListBackups()
	lastVerify, _ := m.LastVerify()
	return Status{
		SourceDir:  m.sourceDir,
		BinaryPath: m.binaryPath,
		Backups:    backups,
		LastVerify: lastVerify,
	}
}

// reExec replaces the current process image with binaryPath, preserving
// argv[1:] and the environment. On success this call never returns (the
// process becomes the new binary, same PID — systemd sees no exit/restart).
// On failure it logs and the current (old) process keeps running.
func reExec(binaryPath string) {
	argv := append([]string{binaryPath}, os.Args[1:]...)
	err := syscall.Exec(binaryPath, argv, os.Environ())
	if err != nil {
		// If we get here, exec failed and the OLD process is still running —
		// there is no partial state to clean up (the binary swap already
		// completed on disk; the next natural restart, e.g. via systemd or a
		// manual restart, will pick up the new binary).
		fmt.Fprintf(os.Stderr, "selfmodify: re-exec into %s failed: %v\n", binaryPath, err)
	}
}
