package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/arisros/tmux-agent-deck/internal/machine"
	"github.com/arisros/tmux-agent-deck/internal/usage"
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
	// Confirming is set after "x" or "i": the next key decides whether the
	// agent under the cursor is killed or interrupted.
	Confirming bool
	pending    Outcome
	// Composing is set after "p": keys go to Draft until enter sends it to
	// the agent under the cursor, or esc drops it.
	Composing bool
	Draft     string
	// Renaming is set after "r": keys go to Draft until enter names the
	// agent under the cursor, an empty name giving it back its own title.
	Renaming bool
	// Reply is what a Send, Answer or Rename outcome asks the caller to deliver.
	Reply string
	// Note is a one-line result of the last action, shown until the next key.
	Note string
	// Timeline is the recent state changes of the previewed agent.
	Timeline []string
	// Attention, toggled with "a", keeps only the agents that need the
	// user: waiting or done.
	Attention bool
	// PreviewOf is the pane whose screen Preview holds; the popup shows it
	// under the list while that pane is the one under the cursor.
	PreviewOf string
	Preview   []string
	// Current is the pane the user is in, marked apart from the cursor.
	Current string
	// Top is the first row the sidebar shows; it only moves to keep the
	// cursor (or, unfocused, the current pane) in view, so redraws never jump.
	Top int
	// scrolled is set by the wheel, which usually reaches an unfocused
	// sidebar: the view then stays where the user put it until keys or a
	// pane switch take over again.
	scrolled    bool
	lastCurrent string
	// Limits is the plan's usage, when Claude has reported it.
	Limits *usage.Limits
	// Now is the clock the views render ages and resets against.
	Now time.Time
}

// Visible is All narrowed by the filter and the attention toggle.
func (l *List) Visible() []Row { return Filter(l.All, l.Filter, l.Attention) }

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
	// Seen marks a done agent as looked at, without jumping to it.
	Seen
	// Interrupt stops the agent's turn, as Esc in its pane would.
	Interrupt
	// Send delivers List.Reply to the agent as a prompt.
	Send
	// Answer presses the one key in List.Reply in the agent's dialog.
	Answer
	// Copy puts the agent's recent output in the tmux paste buffer.
	Copy
	// Rename labels the agent List.Reply; empty removes the label.
	Rename
)

// SetPreview stores pane's screen for the popup to show, without the blank
// lines a screen usually ends in.
func (l *List) SetPreview(pane, screen string) {
	l.PreviewOf = pane
	l.Preview = strings.Split(strings.TrimRight(screen, "\n \t"), "\n")
}

// previewing reports whether the row under the cursor is the one on show: a
// dialog is only answered while the user can read it.
func (l *List) previewing() bool {
	r, ok := l.Selected()
	return ok && l.PreviewOf != "" && r.ID == l.PreviewOf
}

// Handle applies a key: vim motions and arrows, "/" to filter, enter to jump,
// and the actions on the agent under the cursor.
func (l *List) Handle(k Key) Outcome {
	switch k.Name {
	case "wheelup", "wheeldown":
		d := 1
		if k.Name == "wheelup" {
			d = -1
		}
		l.Cursor += d
		l.Top += d
		l.scrolled = true
		l.clamp(len(l.Visible()))
		return Stay
	}
	l.scrolled = false
	l.Note = ""
	if l.Confirming {
		l.Confirming = false
		if k.Rune == 'y' || k.Rune == 'Y' {
			return l.pending
		}
		return Stay
	}
	if l.Composing || l.Renaming {
		switch {
		case k.Name == "enter":
			renaming := l.Renaming
			l.Composing, l.Renaming = false, false
			l.Reply, l.Draft = l.Draft, ""
			if renaming {
				return Rename
			}
			if l.Reply != "" {
				return Send
			}
		case k.Name == "esc":
			l.Composing, l.Renaming, l.Draft = false, false, ""
		case k.Name == "backspace":
			if r := []rune(l.Draft); len(r) > 0 {
				l.Draft = string(r[:len(r)-1])
			}
		case k.Name == "ctrl-c":
			return Quit
		case k.Rune != 0:
			l.Draft += string(k.Rune)
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
	case k.Rune == 'a':
		l.Attention = !l.Attention
		l.Cursor = 0
	case k.Rune == 'x':
		if _, ok := l.Selected(); ok {
			l.Confirming, l.pending = true, Kill
		}
	case k.Rune == 'i':
		if r, ok := l.Selected(); ok && (r.State == machine.Running || r.State == machine.Waiting) {
			l.Confirming, l.pending = true, Interrupt
		}
	case k.Rune == 's':
		if r, ok := l.Selected(); ok && r.State == machine.Done {
			return Seen
		}
	case k.Rune == 'p':
		if _, ok := l.Selected(); ok {
			l.Composing = true
		}
	case k.Rune == 'r':
		if r, ok := l.Selected(); ok {
			l.Renaming, l.Draft = true, r.Label
		}
	case k.Rune == 'y':
		if _, ok := l.Selected(); ok {
			return Copy
		}
	case k.Rune >= '1' && k.Rune <= '9':
		if r, ok := l.Selected(); ok && r.State == machine.Waiting && l.previewing() {
			l.Reply = string(k.Rune)
			return Answer
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
	lines := []string{Fit(" "+bold+"agents"+reset+"   "+Summary(Counts(l.All), true), w)}
	if plan := Plan(l.Limits, l.now(), true); plan != "" {
		lines = append(lines, Fit(" "+bold+"plan"+reset+"     "+plan, w))
	}
	lines = append(lines, dim+strings.Repeat("─", w)+reset)
	// " ◆ " + state + age + target + name + ctx + tokens + cost + folder,
	// one space between columns.
	// The last column is the folder, and grows to hold a branch once any
	// agent works in a repository.
	whereW := 14
	for _, r := range rows {
		if r.Branch != "" {
			whereW = 26
			break
		}
	}
	nameW := w - 3 - (labelW + 1) - (4 + 1) - (20 + 1) - 1 - (10 + 1) - (7 + 1) - (7 + 1) - whereW
	if nameW < 8 {
		nameW = 8
	}
	// The list keeps the lines its rows need, and at least half; the screen
	// of the agent under the cursor gets the rest.
	body, shown := h-len(lines)-1, 0
	if l.PreviewOf != "" && body >= 12 {
		need := len(rows)
		if need < 1 {
			need = 1
		}
		if shown = body - need; shown < body/2 {
			shown = body / 2
		}
		body -= shown
	}
	start := 0
	if cur := l.clamp(len(rows)); cur >= body {
		start = cur - body + 1
	}
	for i := start; i < len(rows) && i < start+body; i++ {
		r := rows[i]
		st := StyleOf(r.State)
		name := r.Name
		if tags := r.Tags(); tags != "" {
			name = dim + tags + reset + name
		}
		line := fmt.Sprintf("%s%s%s%s %s %s %s %s %s %s",
			l.marker(r), st.Color, st.Glyph, reset,
			stateLabel(r), Fit(Age(r.Age), 4), Fit(r.Target(), 20), Fit(name, nameW),
			UsageCols(r),
			dim+Fit(r.Where(), whereW)+reset)
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
	for len(lines) < h-1-shown {
		lines = append(lines, "")
	}
	if shown > 0 {
		lines = append(lines, previewLines(l, w, shown)...)
	}
	lines = append(lines, footer(l, w, "enter jump · / filter · a attention · p send · 1-9 answer · i interrupt · s seen · y copy · r rename · x kill · q close"))
	return lines
}

// previewLines is the bottom of the selected agent's screen under a rule
// that names it, exactly n lines.
func previewLines(l *List, w, n int) []string {
	title := ""
	var screen []string
	if r, ok := l.Selected(); ok && l.previewing() {
		title, screen = " "+r.Name+" ", l.Preview
		if r.State == machine.Waiting && r.Reason != "" {
			title += "· " + r.Reason + " "
		}
		if r.Started > 0 {
			title += "· session " + Age(l.now().Sub(time.Unix(r.Started, 0))) + " "
		}
	}
	out := []string{dim + Fit("──"+title+strings.Repeat("─", w), w) + reset}
	// The last few state changes sit above the screen, when there is room
	// for both.
	if l.previewing() && n >= 12 {
		recent := l.Timeline
		if len(recent) > 4 {
			recent = recent[len(recent)-4:]
		}
		for _, line := range recent {
			out = append(out, dim+Fit(" "+line, w)+reset)
		}
		if len(recent) > 0 {
			out = append(out, dim+" "+strings.Repeat("┄", w-1)+reset)
		}
	}
	if room := n - len(out); len(screen) > room {
		screen = screen[len(screen)-room:]
	}
	for _, line := range screen {
		out = append(out, Fit(" "+strings.ReplaceAll(line, "\t", " "), w))
	}
	for len(out) < n {
		out = append(out, "")
	}
	return out
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
	plan := PlanLines(l.Limits, l.now(), w-1)
	body := h - 2 - 3 - len(plan) // header above; plan, other sessions and help below
	first, last := l.window(rows, cur, focused, body)
	if first > 0 {
		lines = append(lines, dim+Fit(fmt.Sprintf("  ↑ %d more", first), w)+reset)
	}
	for i := first; i < last; i++ {
		r := rows[i]
		st := StyleOf(r.State)
		label := r.Name
		if r.Agent != "" && r.Agent != "claude" {
			label = r.Agent + " · " + label
		}
		head := l.marker(r) + st.Color + st.Glyph + reset + " " + Fit(label, w-3)
		where := filepath.Base(r.Path)
		if r.Branch != "" {
			where = r.Branch
		}
		detail := r.Window + "." + r.Index + " · " + Age(r.Age) + " · " + where
		if r.Usage != nil && r.Usage.ContextUsed != nil {
			detail = r.Window + "." + r.Index + " · " + Bar(*r.Usage.ContextUsed, 5) + dim +
				fmt.Sprintf(" %.0f%%", *r.Usage.ContextUsed) + " · " + Age(r.Age)
		}
		if r.State == machine.Waiting && r.Reason != "" {
			detail = r.Window + "." + r.Index + " · " + r.Reason + " · " + Age(r.Age)
		}
		sub := l.marker(r) + dim + "  " + Fit(detail, w-3) + reset
		switch {
		case focused && i == cur:
			head = reverse + " " + st.Glyph + " " + Fit(label, w-3)
		case r.ID == l.Current:
			head = hereBar + hereBg + st.Color + st.Glyph + reset + hereBg + " " + bold + Fit(label, w-3) + reset
			sub = hereBar + hereBg + dim + "  " + Fit(detail, w-3) + reset
		}
		lines = append(lines, head, sub)
	}
	if last < len(rows) {
		lines = append(lines, dim+Fit(fmt.Sprintf("  ↓ %d more", len(rows)-last), w)+reset)
	}
	if len(rows) == 0 {
		lines = append(lines, dim+" no agents here"+reset)
	}
	for len(lines) < h-3-len(plan) {
		lines = append(lines, "")
	}
	lines = append(lines, dim+strings.Repeat("─", w)+reset)
	for _, p := range plan {
		lines = append(lines, " "+Fit(p, w-1))
	}
	// Agents in the other tmux sessions, which this per-session list omits.
	other := "other sessions: " + Summary(Counts(others), true)
	if len(others) == 0 {
		other = "other sessions: none"
	}
	lines = append(lines, " "+Fit(other, w-1))
	help := "enter · p send · i · s · y · r · x · q"
	if !focused {
		help = "C-h to pick"
	}
	lines = append(lines, footer(l, w, help))
	return lines
}

func footer(l *List, w int, help string) string {
	if l.Confirming {
		if r, ok := l.Selected(); ok {
			verb := "kill "
			if l.pending == Interrupt {
				verb = "interrupt "
			}
			return "\x1b[1;31m" + " " + Fit(verb+r.Name+"? y/n", w-1) + reset
		}
	}
	if l.Composing {
		to := ""
		if r, ok := l.Selected(); ok {
			to = r.Name
		}
		return " " + Fit(dim+"to "+to+" > "+reset+l.Draft+"▏", w-1)
	}
	if l.Renaming {
		return " " + Fit(dim+"name (empty to reset) > "+reset+l.Draft+"▏", w-1)
	}
	if l.Note != "" {
		return " " + Fit(l.Note, w-1)
	}
	if l.Attention && !l.Filtering && l.Filter == "" {
		help = "needing you only, a for all · " + help
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

// labelW fits the longest reason a waiting agent shows, "permission".
const labelW = 10

// stateLabel is the state column; a waiting agent's label shouts, and names
// what it waits for when the deck knows.
func stateLabel(r Row) string {
	if r.State != machine.Waiting {
		return Fit(r.State, labelW)
	}
	label := r.State
	if cause, _ := r.Why(); cause != "" {
		label = cause
	}
	return "\x1b[1;31m" + Fit(label, labelW) + reset
}

// window picks the rows the sidebar shows (two lines each) in body lines,
// scrolling only as far as needed to keep the focus row in view.
func (l *List) window(rows []Row, cur int, focused bool, body int) (int, int) {
	if len(rows)*2 <= body {
		l.Top = 0
		return 0, len(rows)
	}
	fit := (body - 2) / 2 // leave room for the ↑ and ↓ lines
	if fit < 1 {
		fit = 1
	}
	if l.Current != l.lastCurrent {
		l.lastCurrent, l.scrolled = l.Current, false
	}
	focus := cur
	if l.scrolled {
		focus = -1
	}
	if !focused && !l.scrolled {
		focus = -1
		for i, r := range rows {
			if r.ID == l.Current {
				focus = i
			}
		}
	}
	if focus >= 0 {
		if focus < l.Top {
			l.Top = focus
		}
		if focus >= l.Top+fit {
			l.Top = focus - fit + 1
		}
	}
	if l.Top > len(rows)-fit {
		l.Top = len(rows) - fit
	}
	if l.Top < 0 {
		l.Top = 0
	}
	return l.Top, l.Top + fit
}

func (l *List) now() time.Time {
	if l.Now.IsZero() {
		return time.Now()
	}
	return l.Now
}
