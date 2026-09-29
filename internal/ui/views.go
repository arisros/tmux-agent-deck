package ui

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	dim     = "\x1b[90m"
	bold    = "\x1b[1m"
	reverse = "\x1b[7m"
	// here marks the pane the user is in: a bar and a quiet background.
	hereBar = "\x1b[1;36m▌\x1b[0m"
	hereBg  = "\x1b[48;5;236m"
)

// marker is the left gutter of a row: a bar for the pane the user is in.
func (l *List) marker(r Row) string {
	if l.Current != "" && r.ID == l.Current {
		return hereBar
	}
	return " "
}

// List is a cursor over rows with an optional text filter.
type List struct {
	All       []Row
	Cursor    int
	Filter    string
	Filtering bool
	// Confirming is set after "x": the next key decides whether the agent
	// under the cursor is killed.
	Confirming bool
	// Current is the pane the user is in, marked apart from the cursor.
	Current string
}

// Visible is All narrowed by the filter.
func (l *List) Visible() []Row {
	if l.Filter == "" {
		return l.All
	}
	f := strings.ToLower(l.Filter)
	var out []Row
	for _, r := range l.All {
		hay := strings.ToLower(r.Name + " " + r.Target() + " " + r.Path + " " + r.State)
		if strings.Contains(hay, f) {
			out = append(out, r)
		}
	}
	return out
}

// Selected is the row under the cursor.
func (l *List) Selected() (Row, bool) {
	v := l.Visible()
	if len(v) == 0 {
		return Row{}, false
	}
	return v[l.clamp(len(v))], true
}

func (l *List) clamp(n int) int {
	if l.Cursor >= n {
		l.Cursor = n - 1
	}
	if l.Cursor < 0 {
		l.Cursor = 0
	}
	return l.Cursor
}

// Keep moves the cursor back onto pane after a refresh reorders rows.
func (l *List) Keep(pane string) {
	for i, r := range l.Visible() {
		if r.ID == pane {
			l.Cursor = i
			return
		}
	}
}

// Outcome of a keypress.
type Outcome int

// Outcomes.
const (
	Stay Outcome = iota
	Jump
	Quit
	Kill
)

// Handle applies a key: vim motions and arrows, "/" to filter, enter to jump.
func (l *List) Handle(k Key) Outcome {
	if l.Confirming {
		l.Confirming = false
		if k.Rune == 'y' || k.Rune == 'Y' {
			return Kill
		}
		return Stay
	}
	if l.Filtering {
		switch {
		case k.Name == "enter" || k.Name == "down" || k.Name == "up":
			l.Filtering = false
			if k.Name == "enter" {
				return Jump
			}
		case k.Name == "esc":
			l.Filtering, l.Filter = false, ""
		case k.Name == "backspace":
			if r := []rune(l.Filter); len(r) > 0 {
				l.Filter = string(r[:len(r)-1])
			}
		case k.Name == "ctrl-c":
			return Quit
		case k.Rune != 0:
			l.Filter += string(k.Rune)
			l.Cursor = 0
		}
		return Stay
	}
	switch {
	case k.Name == "down" || k.Rune == 'j':
		l.Cursor++
	case k.Name == "up" || k.Rune == 'k':
		l.Cursor--
	case k.Rune == 'g':
		l.Cursor = 0
	case k.Rune == 'G':
		l.Cursor = len(l.Visible()) - 1
	case k.Name == "enter" || k.Name == "right" || k.Rune == 'l':
		return Jump
	case k.Rune == '/':
		l.Filtering = true
	case k.Rune == 'x':
		if _, ok := l.Selected(); ok {
			l.Confirming = true
		}
	case k.Name == "esc" || k.Rune == 'q' || k.Name == "ctrl-c":
		return Quit
	}
	l.clamp(len(l.Visible()))
	return Stay
}

// Popup renders the all-sessions list.
func Popup(l *List, w, h int) []string {
	rows := l.Visible()
	lines := []string{
		Fit(" "+bold+"agents"+reset+"   "+Summary(Counts(l.All), true), w),
		dim + strings.Repeat("─", w) + reset,
	}
	// " ◆ " + state + age + target + name + folder, one space between columns.
	nameW := w - 3 - (8 + 1) - (4 + 1) - (20 + 1) - 1 - 18
	if nameW < 8 {
		nameW = 8
	}
	body := h - 3
	start := 0
	if cur := l.clamp(len(rows)); cur >= body {
		start = cur - body + 1
	}
	for i := start; i < len(rows) && i < start+body; i++ {
		r := rows[i]
		st := Styles[r.State]
		line := fmt.Sprintf("%s%s%s%s %s %s %s %s %s",
			l.marker(r), st.Color, st.Glyph, reset,
			Fit(r.State, 8), Fit(Age(r.Age), 4), Fit(r.Target(), 20), Fit(r.Name, nameW),
			dim+Fit(filepath.Base(r.Path), 18)+reset)
		switch {
		case i == l.Cursor:
			line = reverse + stripReset(line)
		case r.ID == l.Current:
			line = hereBar + hereBg + strings.ReplaceAll(strings.TrimPrefix(line, hereBar), reset, reset+hereBg)
		}
		lines = append(lines, Fit(line, w))
	}
	if len(rows) == 0 {
		lines = append(lines, dim+" no agents"+reset)
	}
	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	lines = append(lines, footer(l, w, "j/k move · enter jump · / filter · x kill · q close"))
	return lines
}

// Sidebar renders one session's agents in a narrow column, plus a summary of
// the other sessions.
func Sidebar(l *List, others []Row, session string, focused bool, w, h int) []string {
	lines := []string{
		Fit(" "+bold+"agents"+reset+dim+" · "+session+reset, w),
		dim + strings.Repeat("─", w) + reset,
	}
	rows := l.Visible()
	cur := l.clamp(len(rows))
	for i, r := range rows {
		if len(lines)+2 > h-3 {
			break
		}
		st := Styles[r.State]
		head := l.marker(r) + st.Color + st.Glyph + reset + " " + Fit(r.Name, w-3)
		sub := l.marker(r) + dim + "  " + Fit(r.Window+"."+r.Index+" · "+filepath.Base(r.Path)+" · "+Age(r.Age), w-3) + reset
		switch {
		case focused && i == cur:
			head = reverse + " " + st.Glyph + " " + Fit(r.Name, w-3)
		case r.ID == l.Current:
			head = hereBar + hereBg + st.Color + st.Glyph + reset + hereBg + " " + bold + Fit(r.Name, w-3) + reset
			sub = hereBar + hereBg + dim + "  " + Fit(r.Window+"."+r.Index+" · "+filepath.Base(r.Path)+" · "+Age(r.Age), w-3) + reset
		}
		lines = append(lines, head, sub)
	}
	if len(rows) == 0 {
		lines = append(lines, dim+" no agents here"+reset)
	}
	for len(lines) < h-3 {
		lines = append(lines, "")
	}
	lines = append(lines, dim+strings.Repeat("─", w)+reset)
	other := "other: " + Summary(Counts(others), false)
	if len(others) == 0 {
		other = "other: none"
	}
	lines = append(lines, " "+Fit(other, w-1))
	help := "j/k · enter · x kill · q"
	if !focused {
		help = "C-h to pick"
	}
	lines = append(lines, footer(l, w, help))
	return lines
}

func footer(l *List, w int, help string) string {
	if l.Confirming {
		if r, ok := l.Selected(); ok {
			return "\x1b[1;31m" + " " + Fit("kill "+r.Name+"? y/n", w-1) + reset
		}
	}
	if l.Filtering || l.Filter != "" {
		cursor := ""
		if l.Filtering {
			cursor = "▏"
		}
		return " /" + Fit(l.Filter+cursor, w-2)
	}
	return dim + " " + Fit(help, w-1) + reset
}

func stripReset(s string) string {
	s = strings.ReplaceAll(s, reset, "")
	for _, st := range Styles {
		s = strings.ReplaceAll(s, st.Color, "")
	}
	return strings.ReplaceAll(s, dim, "")
}
