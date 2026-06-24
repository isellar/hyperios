package governor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/isellar/hyperios/internal/governor/capability"
	"github.com/isellar/hyperios/internal/types"
)

type PopupHandler interface {
	ShowPopup(toolID, scope string) (string, error)
}

type ToolAuthorization struct {
	registry    *capability.Registry
	storagePath string
	mu          sync.RWMutex
	auths       map[string]types.ToolAuthorization
	popup       PopupHandler
}

func NewToolAuthorization(registry *capability.Registry, storagePath string) *ToolAuthorization {
	return &ToolAuthorization{
		registry:    registry,
		storagePath: storagePath,
		auths:       make(map[string]types.ToolAuthorization),
	}
}

func (ta *ToolAuthorization) SetPopupHandler(handler PopupHandler) {
	ta.mu.Lock()
	defer ta.mu.Unlock()
	ta.popup = handler
}

func (ta *ToolAuthorization) Load() error {
	ta.mu.Lock()
	defer ta.mu.Unlock()

	if ta.storagePath == "" {
		return nil
	}

	data, err := os.ReadFile(ta.storagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("load tool authorizations: %w", err)
	}

	var auths []types.ToolAuthorization
	if err := json.Unmarshal(data, &auths); err != nil {
		return fmt.Errorf("parse tool authorizations: %w", err)
	}

	now := time.Now()
	for _, auth := range auths {
		if auth.Scope == "always" || (auth.ExpiresAt.IsZero() || now.Before(auth.ExpiresAt)) {
			ta.auths[auth.ToolID] = auth
		}
	}

	return nil
}

func (ta *ToolAuthorization) save() error {
	if ta.storagePath == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(ta.storagePath), 0700); err != nil {
		return fmt.Errorf("create auth storage dir: %w", err)
	}

	auths := make([]types.ToolAuthorization, 0, len(ta.auths))
	now := time.Now()
	for _, auth := range ta.auths {
		if auth.Scope == "always" || (auth.ExpiresAt.IsZero() || now.Before(auth.ExpiresAt)) {
			auths = append(auths, auth)
		}
	}

	data, err := json.MarshalIndent(auths, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tool authorizations: %w", err)
	}

	if err := os.WriteFile(ta.storagePath, data, 0600); err != nil {
		return fmt.Errorf("write tool authorizations: %w", err)
	}

	return nil
}

func (ta *ToolAuthorization) RequestAuthorization(toolID string, scope string) error {
	ta.mu.Lock()
	defer ta.mu.Unlock()

	var expiresAt time.Time
	switch scope {
	case "always":
		expiresAt = time.Time{}
	case "session":
		expiresAt = time.Now().Add(24 * time.Hour)
	case "request":
		expiresAt = time.Now().Add(1 * time.Hour)
	default:
		return fmt.Errorf("unknown authorization scope: %q (must be always, session, or request)", scope)
	}

	auth := types.ToolAuthorization{
		ToolID:       toolID,
		Scope:        scope,
		ExpiresAt:    expiresAt,
		AuthorizedBy: "user",
	}

	ta.auths[toolID] = auth
	return ta.save()
}

func (ta *ToolAuthorization) CheckAuthorization(toolID string) bool {
	ta.mu.RLock()
	defer ta.mu.RUnlock()

	auth, ok := ta.auths[toolID]
	if !ok {
		return false
	}

	if auth.Scope == "always" {
		return true
	}

	if auth.ExpiresAt.IsZero() {
		return true
	}

	return time.Now().Before(auth.ExpiresAt)
}

func (ta *ToolAuthorization) RevokeAuthorization(toolID string) error {
	ta.mu.Lock()
	defer ta.mu.Unlock()

	delete(ta.auths, toolID)
	return ta.save()
}

func (ta *ToolAuthorization) FirePopup(toolID string, scope string) (string, error) {
	ta.mu.RLock()
	handler := ta.popup
	ta.mu.RUnlock()

	if handler == nil {
		return "", fmt.Errorf("no popup handler registered")
	}

	return handler.ShowPopup(toolID, scope)
}

func (ta *ToolAuthorization) ListAuthorizations() []types.ToolAuthorization {
	ta.mu.RLock()
	defer ta.mu.RUnlock()

	auths := make([]types.ToolAuthorization, 0, len(ta.auths))
	now := time.Now()
	for _, auth := range ta.auths {
		if auth.Scope == "always" || (auth.ExpiresAt.IsZero() || now.Before(auth.ExpiresAt)) {
			auths = append(auths, auth)
		}
	}
	return auths
}
