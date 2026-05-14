package shell

import "github.com/charmbracelet/lipgloss"

// Colour palette — monochrome base with intentional accent colours.
// The terminal is a primary interface, not a decoration. Colours signal
// meaning: green = success, yellow = caution, red = blocked/failed,
// cyan = system/informational, white = output.
var (
	colGreen  = lipgloss.Color("2")
	colYellow = lipgloss.Color("3")
	colRed    = lipgloss.Color("1")
	colCyan   = lipgloss.Color("6")
	colGray   = lipgloss.Color("8")
	colWhite  = lipgloss.Color("15")
	colBlue   = lipgloss.Color("4")
)

var (
	// Shell chrome
	styleHeader = lipgloss.NewStyle().
			Foreground(colCyan).
			Bold(true)

	styleDivider = lipgloss.NewStyle().
			Foreground(colGray)

	stylePrompt = lipgloss.NewStyle().
			Foreground(colCyan).
			Bold(true)

	// Output area
	styleOutput = lipgloss.NewStyle().
			Foreground(colWhite)

	styleSystem = lipgloss.NewStyle().
			Foreground(colCyan).
			Italic(true)

	styleError = lipgloss.NewStyle().
			Foreground(colRed)

	// Step events
	styleStepStarted = lipgloss.NewStyle().
				Foreground(colGray)

	styleStepOk = lipgloss.NewStyle().
			Foreground(colGreen)

	styleStepFail = lipgloss.NewStyle().
			Foreground(colRed)

	styleStepSkip = lipgloss.NewStyle().
			Foreground(colGray).
			Italic(true)

	// Verdict badges
	styleApproved = lipgloss.NewStyle().
			Foreground(colGreen).
			Bold(true)

	styleModified = lipgloss.NewStyle().
			Foreground(colYellow).
			Bold(true)

	styleBlocked = lipgloss.NewStyle().
			Foreground(colRed).
			Bold(true)

	// Plan summary
	stylePlanHeading = lipgloss.NewStyle().
				Foreground(colBlue).
				Bold(true)

	stylePlanStep = lipgloss.NewStyle().
			Foreground(colWhite)

	// Approval prompt
	styleApprovalPrompt = lipgloss.NewStyle().
				Foreground(colYellow).
				Bold(true)

	styleApprovalHint = lipgloss.NewStyle().
				Foreground(colGray)

	// Notification banner (halted/in-progress sessions)
	styleBanner = lipgloss.NewStyle().
			Foreground(colYellow).
			Bold(true).
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(colYellow).
			PaddingLeft(1)

	// Muted capability type line
	styleGray = lipgloss.NewStyle().
			Foreground(colGray)

	// Command output block
	styleCommandOutput = lipgloss.NewStyle().
				Foreground(colGray).
				PaddingLeft(2)
)

// verdictStyle returns the appropriate style for a verdict string.
func verdictStyle(verdict string) lipgloss.Style {
	switch verdict {
	case "approved":
		return styleApproved
	case "modified":
		return styleModified
	case "blocked":
		return styleBlocked
	default:
		return styleOutput
	}
}

// verdictIcon returns a single-character icon for a verdict.
func verdictIcon(verdict string) string {
	switch verdict {
	case "approved":
		return "+"
	case "modified":
		return "~"
	case "blocked":
		return "✗"
	default:
		return "?"
	}
}
