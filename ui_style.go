package main

import (
	"fmt"
	"io"

	"github.com/charmbracelet/lipgloss"
)

// Styles for interactive install prompts. lipgloss auto-detects the terminal's color
// profile and safely degrades to plain text on non-TTY output (e.g. piped or CI runs).
var (
	styleMenuTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	styleMenuNum   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	styleMenuName  = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	styleMenuHint  = lipgloss.NewStyle().Faint(true)
	stylePrompt    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	styleSuccess   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	styleError     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
)

// printMenuTitle renders a styled heading above a numbered menu.
func printMenuTitle(stdout io.Writer, text string) {
	_, _ = fmt.Fprintln(stdout, styleMenuTitle.Render(text))
}

// printMenuItem renders one numbered menu row, with an optional dimmed detail suffix.
func printMenuItem(stdout io.Writer, num int, name, detail string) {
	numStr := styleMenuNum.Render(fmt.Sprintf("%2d)", num))
	if detail == "" {
		_, _ = fmt.Fprintf(stdout, "  %s %s\n", numStr, styleMenuName.Render(name))
		return
	}
	_, _ = fmt.Fprintf(stdout, "  %s %s %s\n", numStr, styleMenuName.Render(name), styleMenuHint.Render("- "+detail))
}

// printPrompt renders a styled inline "label:  " prompt with no trailing newline.
func printPrompt(stdout io.Writer, label string) {
	_, _ = fmt.Fprint(stdout, stylePrompt.Render(label+":")+"  ")
}

// printSuccess renders a styled confirmation line.
func printSuccess(stdout io.Writer, text string) {
	_, _ = fmt.Fprintln(stdout, styleSuccess.Render("✔ "+text))
}

// printError renders a styled inline error line.
func printError(stdout io.Writer, text string) {
	_, _ = fmt.Fprintln(stdout, styleError.Render("✘ "+text))
}

// printHint renders a dimmed helper line.
func printHint(stdout io.Writer, text string) {
	_, _ = fmt.Fprintln(stdout, styleMenuHint.Render(text))
}
