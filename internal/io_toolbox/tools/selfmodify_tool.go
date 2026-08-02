package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/isellar/hyperios/internal/selfmodify"
)

// SelfModifyTool lets an agent rebuild, verify, and apply changes to
// HyperiOS's own source tree — the mechanism for "the agent can improve its
// own code, not just submit goals about it." It is only ever registered when
// explicitly enabled (see io_toolbox.NewIOToolbox and 'hyperi selfmodify
// enable'); nothing here runs unless the user opted in.
//
// Input format: "<action>" where action is one of:
//   - "verify"   — run build+vet+test against the current source tree, report results, apply nothing
//   - "apply"    — verify, and if it passes, install the new binary and restart into it
//   - "rollback" — restore the most recently backed-up binary and restart into it
//   - "status"   — report source dir, binary path, and available rollback points
type SelfModifyTool struct {
	mgr *selfmodify.Manager
}

// NewSelfModifyTool creates a SelfModifyTool backed by mgr.
func NewSelfModifyTool(mgr *selfmodify.Manager) *SelfModifyTool {
	return &SelfModifyTool{mgr: mgr}
}

// Name returns "self_modify".
func (t *SelfModifyTool) Name() string { return "self_modify" }

// Description returns a description of the self-modify tool.
func (t *SelfModifyTool) Description() string {
	return "Use this tool ONLY when your goal explicitly involves modifying HyperiOS's own source code. " +
		"After editing source files with the shell tool, call this to verify and apply the changes. " +
		"Input: 'verify' (run build+vet+tests, no changes applied — always do this first), " +
		"'apply' (verify then install the new binary and restart), " +
		"'rollback' (restore the previous binary), " +
		"'status' (show source path, binary path, available rollback points)."
}

// Execute runs the requested self-modify action.
func (t *SelfModifyTool) Execute(input string) (string, error) {
	action := strings.ToLower(strings.TrimSpace(input))
	ctx := context.Background()

	switch action {
	case "verify":
		result, err := t.mgr.Verify(ctx)
		if err != nil {
			return "", fmt.Errorf("self_modify: verify: %w", err)
		}
		return result.Summary(), nil

	case "apply":
		msg, err := t.mgr.Apply(ctx)
		if err != nil {
			return "", fmt.Errorf("self_modify: apply: %w", err)
		}
		return msg, nil

	case "rollback":
		msg, err := t.mgr.Rollback()
		if err != nil {
			return "", fmt.Errorf("self_modify: rollback: %w", err)
		}
		return msg, nil

	case "status":
		status := t.mgr.GetStatus()
		data, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			return "", fmt.Errorf("self_modify: marshal status: %w", err)
		}
		return string(data), nil

	default:
		return "", fmt.Errorf("self_modify: unknown action %q; expected 'verify', 'apply', 'rollback', or 'status'", action)
	}
}
