// Package uistyle renders lipgloss-styled output for interactive install prompts.
// lipgloss auto-detects the terminal's color profile and safely degrades to plain
// text on non-TTY output (e.g. piped or CI runs).
package uistyle

import (
	"fmt"
	"io"

	"github.com/charmbracelet/lipgloss"
)

var (
	styleMenuTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	styleMenuNum   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	styleMenuName  = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	styleMenuHint  = lipgloss.NewStyle().Faint(true)
	stylePrompt    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	styleSuccess   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	styleError     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
)

// PrintMenuTitle renders a styled heading above a numbered menu.
func PrintMenuTitle(stdout io.Writer, text string) {
	_, _ = fmt.Fprintln(stdout, styleMenuTitle.Render(text))
}

// PrintMenuItem renders one numbered menu row, with an optional dimmed detail suffix.
func PrintMenuItem(stdout io.Writer, num int, name, detail string) {
	numStr := styleMenuNum.Render(fmt.Sprintf("%2d)", num))
	if detail == "" {
		_, _ = fmt.Fprintf(stdout, "  %s %s\n", numStr, styleMenuName.Render(name))
		return
	}
	_, _ = fmt.Fprintf(stdout, "  %s %s %s\n", numStr, styleMenuName.Render(name), styleMenuHint.Render("- "+detail))
}

// PrintPrompt renders a styled inline "label:  " prompt with no trailing newline.
func PrintPrompt(stdout io.Writer, label string) {
	_, _ = fmt.Fprint(stdout, stylePrompt.Render(label+":")+"  ")
}

// PrintSuccess renders a styled confirmation line.
func PrintSuccess(stdout io.Writer, text string) {
	_, _ = fmt.Fprintln(stdout, styleSuccess.Render("✔ "+text))
}

// PrintError renders a styled inline error line.
func PrintError(stdout io.Writer, text string) {
	_, _ = fmt.Fprintln(stdout, styleError.Render("✘ "+text))
}

// PrintHint renders a dimmed helper line.
func PrintHint(stdout io.Writer, text string) {
	_, _ = fmt.Fprintln(stdout, styleMenuHint.Render(text))
}
