// Package ui renders the popup and the sidebar: the same agent rows, drawn
// with plain ANSI escapes on a raw terminal.
package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/arisros/tmux-agent-deck/internal/machine"
	"github.com/arisros/tmux-agent-deck/internal/tmux"
)

// Row is one agent.
type Row struct {
	tmux.Pane
	Name string
	Age  time.Duration
}

// Target is the tmux address shown for a row.
func (r Row) Target() string { return r.Session + ":" + r.Window + "." + r.Index }

var priority = map[string]int{machine.Waiting: 0, machine.Done: 1, machine.Running: 2, machine.Idle: 3}

// Agents keeps the panes that run a live Claude session the deck knows about,
// most urgent first. A pane whose Claude died keeps its options until the
// next hook, so the process check is what hides it.
func Agents(panes []tmux.Pane, now time.Time) []Row {
	var rows []Row
	for _, p := range panes {
		if p.State == "" || p.Sidebar != "" || !tmux.IsClaude(p.Command) {
			continue
		}
		age := time.Duration(0)
		if p.Since > 0 {
			age = now.Sub(time.Unix(p.Since, 0))
		}
		rows = append(rows, Row{Pane: p, Name: name(p), Age: age})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if priority[a.State] != priority[b.State] {
			return priority[a.State] < priority[b.State]
		}
		if a.Session != b.Session {
			return a.Session < b.Session
		}
		if a.Window != b.Window {
			return num(a.Window) < num(b.Window)
		}
		return num(a.Index) < num(b.Index)
	})
	return rows
}

func num(s string) int { n, _ := strconv.Atoi(s); return n }

// name is Claude's session title from the terminal title, without the status
// glyph Claude prefixes; the folder when there is no title yet.
func name(p tmux.Pane) string {
	t := strings.TrimLeftFunc(p.Title, func(r rune) bool {
		return unicode.IsSpace(r) || (r > 0x2000 && !unicode.IsLetter(r) && !unicode.IsDigit(r))
	})
	if t == "" || strings.Contains(t, ".local") {
		return filepath.Base(p.Path)
	}
	return t
}

// Style is the glyph and color of a state.
type Style struct{ Glyph, Color string }

// Styles per state, matching the tmux formats set by tmux-init. Waiting is a
// red badge because it is the one state that needs a decision; running is
// green and pulses in the views (see Pulse).
var Styles = map[string]Style{
	machine.Waiting: {"◆", "\x1b[1;97;41m"},
	machine.Done:    {"✔", "\x1b[1;34m"},
	machine.Running: {"●", "\x1b[1;92m"},
	machine.Idle:    {"○", "\x1b[90m"},
}

// pulse cycles a running agent's dot from full to hollow and back. The glyph
// changes, not only its shade: a dim-green frame is too close to green to
// read as movement on most terminals.
var pulse = []Style{
	{"●", "\x1b[1;92m"},
	{"◉", "\x1b[92m"},
	{"○", "\x1b[32m"},
	{"◉", "\x1b[92m"},
}

// Pulse is the animation frame the views draw with; they advance it only
// while a running agent is on screen.
var Pulse int

// StyleOf is a state's style at the current animation frame.
func StyleOf(state string) Style {
	if state == machine.Running {
		return pulse[Pulse%len(pulse)]
	}
	return Styles[state]
}

// Animated reports whether rows need the pulse to keep moving.
func Animated(rows []Row) bool {
	for _, r := range rows {
		if r.State == machine.Running {
			return true
		}
	}
	return false
}

const reset = "\x1b[0m"

// Age formats a duration compactly: 42s, 7m, 3h, 2d.
func Age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// Counts summarizes rows per state.
func Counts(rows []Row) map[string]int {
	c := map[string]int{}
	for _, r := range rows {
		c[r.State]++
	}
	return c
}

// Summary is "◆ 1 waiting  ✔ 2 done ..." for non-zero states, colored.
func Summary(c map[string]int, color bool) string {
	var parts []string
	for _, s := range []string{machine.Waiting, machine.Done, machine.Running, machine.Idle} {
		if c[s] == 0 {
			continue
		}
		st := StyleOf(s)
		if color {
			parts = append(parts, fmt.Sprintf("%s%s%s %d %s", st.Color, st.Glyph, reset, c[s], s))
		} else {
			parts = append(parts, fmt.Sprintf("%s %d %s", st.Glyph, c[s], s))
		}
	}
	return strings.Join(parts, "  ")
}

// Fit pads or truncates s to exactly w terminal cells. ANSI escape
// sequences pass through without counting.
func Fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if n := Width(s); n <= w {
		return s + strings.Repeat(" ", w-n)
	}
	var b strings.Builder
	used := 0
	for i := 0; i < len(s); {
		if n := escapeLen(s[i:]); n > 0 {
			b.WriteString(s[i : i+n])
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := cellWidth(r)
		if used+rw > w-1 {
			break
		}
		b.WriteRune(r)
		used += rw
		i += size
	}
	b.WriteRune('…')
	return b.String() + strings.Repeat(" ", w-used-1)
}

// Width is the number of terminal cells s occupies.
func Width(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if e := escapeLen(s[i:]); e > 0 {
			i += e
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		n += cellWidth(r)
		i += size
	}
	return n
}

// escapeLen is the byte length of a CSI sequence at the start of s, or 0.
func escapeLen(s string) int {
	if len(s) < 2 || s[0] != 0x1b || s[1] != '[' {
		return 0
	}
	for i := 2; i < len(s); i++ {
		if s[i] >= 0x40 && s[i] <= 0x7e {
			return i + 1
		}
	}
	return 0
}

func cellWidth(r rune) int {
	switch {
	case r == 0 || unicode.Is(unicode.Mn, r):
		return 0
	case r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) || (r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) || (r >= 0xfe30 && r <= 0xfe4f) || (r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) || (r >= 0x1f300 && r <= 0x1faff) || (r >= 0x20000 && r <= 0x3fffd)):
		return 2
	}
	return 1
}
