package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/nosini/rclone-proxy-tui/internal/state"
)

var (
	colAccent = lipgloss.AdaptiveColor{Light: "#5A3FC0", Dark: "#9D8CFF"}
	colGreen  = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#3FB950"}
	colYellow = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#D29922"}
	colRed    = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F85149"}
	colMuted  = lipgloss.AdaptiveColor{Light: "#6E7781", Dark: "#8B949E"}
	colSelBg  = lipgloss.AdaptiveColor{Light: "#E7E2FA", Dark: "#2D2A45"}

	sTitle     = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	sTab       = lipgloss.NewStyle().Padding(0, 1).Foreground(colMuted)
	sTabActive = lipgloss.NewStyle().Padding(0, 1).Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(colAccent)
	sMuted     = lipgloss.NewStyle().Foreground(colMuted)
	sBold      = lipgloss.NewStyle().Bold(true)
	sKey       = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	sGreen     = lipgloss.NewStyle().Foreground(colGreen)
	sYellow    = lipgloss.NewStyle().Foreground(colYellow)
	sRed       = lipgloss.NewStyle().Foreground(colRed)
	sHeader    = lipgloss.NewStyle().Bold(true).Foreground(colMuted)
	sCursor    = lipgloss.NewStyle().Background(colSelBg).Bold(true)
	sModal     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colAccent).Padding(0, 1)
	sErr       = lipgloss.NewStyle().Foreground(colRed).Bold(true)
)

// pad truncates or pads s to exactly w cells.
func pad(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if n := lipgloss.Width(s); n < w {
		s += strings.Repeat(" ", w-n)
	}
	return s
}

// trunc truncates s to at most w cells.
func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

// window returns the slice [start,end) of n items to show in height rows
// so that cursor is visible.
func window(cursor, n, height int) (int, int) {
	if height <= 0 || n <= height {
		return 0, n
	}
	start := cursor - height/2
	if start < 0 {
		start = 0
	}
	if start+height > n {
		start = n - height
	}
	return start, start + height
}

// hints renders "key action · key action".
func hints(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, sKey.Render(pairs[i])+" "+sMuted.Render(pairs[i+1]))
	}
	return strings.Join(parts, sMuted.Render(" · "))
}

// wrapHints joins hint fragments onto as few lines as fit in width.
func wrapHints(width int, pairs ...string) string {
	var lines []string
	cur := ""
	for i := 0; i+1 < len(pairs); i += 2 {
		h := hints(pairs[i], pairs[i+1])
		if cur == "" {
			cur = h
			continue
		}
		cand := cur + sMuted.Render(" · ") + h
		if lipgloss.Width(cand) > width {
			lines = append(lines, cur)
			cur = h
		} else {
			cur = cand
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}

// dot renders a coloured status indicator for a serve process state.
func dot(ps *state.ProtoStatus, daemonUp bool) string {
	if !daemonUp || ps == nil {
		return sMuted.Render("○")
	}
	switch ps.State {
	case state.ProcRunning:
		return sGreen.Render("●")
	case state.ProcStarting:
		return sYellow.Render("◐")
	case state.ProcError:
		return sRed.Render("✗")
	}
	return sMuted.Render("○")
}

func stateText(ps *state.ProtoStatus, enabled, daemonUp bool) string {
	switch {
	case !enabled:
		return sMuted.Render("disabled")
	case !daemonUp:
		return sMuted.Render("daemon stopped")
	case ps == nil:
		return sMuted.Render("pending")
	case ps.State == state.ProcRunning && ps.Note != "":
		return sGreen.Render("running") + sYellow.Render(" - "+ps.Note)
	case ps.State == state.ProcRunning:
		return sGreen.Render("running")
	case ps.State == state.ProcStarting:
		return sYellow.Render("starting")
	case ps.State == state.ProcError:
		msg := ps.Error
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		return sRed.Render("error: " + msg)
	}
	return sMuted.Render(ps.State)
}

// wordWrap wraps plain text to width.
func wordWrap(s string, width int) string {
	if width <= 10 {
		return s
	}
	return lipgloss.NewStyle().Width(width).Render(s)
}
