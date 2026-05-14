package display

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ATSPIClient queries the Linux accessibility tree (AT-SPI) for semantic
// UI element access without screenshots.
//
// AT-SPI works for native GTK/Qt apps. Electron apps and browser content
// have partial or broken AT-SPI support — use vision model for those.
//
// Implementation uses the `at-spi2-core` tools (`at-spi-bus-launcher`,
// `accerciser` for debugging) and xdotool-style AT-SPI bindings via
// the `atspi` command-line tool where available.
//
// For Phase 4, this uses `gdbus` introspection of the AT-SPI D-Bus interface
// as the query mechanism — no CGO, no external Go bindings needed.
type ATSPIClient struct{}

// NewATSPIClient creates an AT-SPI client.
func NewATSPIClient() *ATSPIClient {
	return &ATSPIClient{}
}

// IsAvailable returns true if AT-SPI infrastructure is running.
func (a *ATSPIClient) IsAvailable() bool {
	// Check if the AT-SPI D-Bus socket is accessible
	_, err := exec.LookPath("gdbus")
	if err != nil {
		return false
	}
	// Try to ping the AT-SPI registry
	cmd := exec.Command("gdbus", "call",
		"--session",
		"--dest", "org.a11y.Bus",
		"--object-path", "/org/a11y/bus",
		"--method", "org.freedesktop.DBus.Peer.Ping",
	)
	return cmd.Run() == nil
}

// Element represents a UI element found in the accessibility tree.
type Element struct {
	Name     string // accessible name (label text, aria-label, etc.)
	Role     string // role: "push button", "text", "menu item", etc.
	AppName  string // application name
	// Geometry (populated when available)
	X, Y, W, H int
}

// FindElement searches the AT-SPI tree for an element matching name and/or role.
// Returns the first match. Uses gdbus to query the accessibility registry.
func (a *ATSPIClient) FindElement(name, role string) (*Element, error) {
	if !a.IsAvailable() {
		return nil, fmt.Errorf("atspi: AT-SPI not available (is at-spi2-core running?)")
	}

	// Query running accessible applications via AT-SPI D-Bus
	out, err := exec.Command("gdbus", "call",
		"--session",
		"--dest", "org.a11y.Bus",
		"--object-path", "/org/a11y/bus",
		"--method", "org.a11y.Bus.GetAddress",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("atspi: get bus address: %w", err)
	}

	busAddr := strings.Trim(strings.TrimSpace(string(out)), "()'")
	if busAddr == "" {
		return nil, fmt.Errorf("atspi: empty bus address")
	}

	// Use the AT-SPI bus address to enumerate accessible objects
	// This is a simplified search — a full implementation would walk the tree
	// For Phase 4, we use xdotool-compatible search via AT-SPI
	result, err := a.searchAccessibleTree(busAddr, name, role)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// searchAccessibleTree performs a D-Bus walk of the AT-SPI tree.
// Returns the first element matching name and/or role criteria.
func (a *ATSPIClient) searchAccessibleTree(busAddr, name, role string) (*Element, error) {
	// AT-SPI tree walk via gdbus introspection of accessible objects
	// The AT-SPI registry root is at org.a11y.atspi.Registry
	out, err := exec.Command("gdbus", "call",
		"--address", busAddr,
		"--dest", "org.a11y.atspi.Registry",
		"--object-path", "/org/a11y/atspi/accessible/root",
		"--method", "org.a11y.atspi.Accessible.GetChildren",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("atspi: get children: %w", err)
	}

	// Parse the D-Bus response to find application nodes
	// Each application exposes its accessible tree under its own bus name
	// For Phase 4 this is a best-effort implementation — full AT-SPI
	// tree walking requires a proper binding library
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if name != "" && strings.Contains(strings.ToLower(line), strings.ToLower(name)) {
			return &Element{Name: name, Role: role}, nil
		}
	}

	return nil, nil
}

// WaitForElement polls until an element matching criteria appears or timeout expires.
// This is the implementation for ReadyCondition type "atspi:present".
func (a *ATSPIClient) WaitForElement(name, role string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		elem, err := a.FindElement(name, role)
		if err == nil && elem != nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("atspi: element %q (role: %q) did not appear within %v", name, role, timeout)
}

// Click sends a click action to a named element via AT-SPI.
// Preferred over ydotool for native apps as it uses semantic addressing.
func (a *ATSPIClient) Click(name, role string) error {
	elem, err := a.FindElement(name, role)
	if err != nil {
		return err
	}
	if elem == nil {
		return fmt.Errorf("atspi: element %q not found", name)
	}

	// Send DoAction via AT-SPI (action 0 = default action, usually "click")
	// Full implementation requires knowing the object path of the element
	// Phase 4 placeholder — real implementation needs the object path from FindElement
	return fmt.Errorf("atspi: Click not yet fully implemented (element found: %s)", elem.Name)
}
