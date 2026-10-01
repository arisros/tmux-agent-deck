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
	"github.com/arisros/tmux-agent-deck/internal/usage"
)

// Row is one agent.
type Row struct {
	tmux.Pane
	Name string
	Age  time.Duration
	// Branch is the git branch checked out where the agent works, when a
	// view has looked it up.
	Branch string
	Usage  *usage.Session // nil until the agent has reported usage, which only Claude Code does
}

// Attach joins each row to the usage its Claude session reported: by session
// id when the deck has one, otherwise by the pane Claude reported from, most
// recent report first (a pane can host several sessions over time).
func Attach(rows []Row, sessions map[string]usage.Session) []Row {
	byPane := map[string]usage.Session{}
	for _, s := range sessions {
		if s.Pane == "" {
			continue
		}
		if prev, ok := byPane[s.Pane]; !ok || s.UpdatedAtUnix > prev.UpdatedAtUnix {
			byPane[s.Pane] = s
		}
	}
	for i := range rows {
		if s, ok := sessions[rows[i].SID]; ok && rows[i].SID != "" {
			rows[i].Usage = &s
		} else if s, ok := byPane[rows[i].ID]; ok {
			rows[i].Usage = &s
		}
	}
	return rows
}

// Why splits the reason a waiting agent carries into its cause and the tool
// it concerns: "permission Bash" is permission for Bash.
func (r Row) Why() (cause, tool string) {
	cause, tool, _ = strings.Cut(r.Reason, " ")
	return cause, tool
}

// Branches fills each row's branch through lookup, asking once per path.
func Branches(rows []Row, lookup func(dir string) string) []Row {
	seen := map[string]string{}
	for i := range rows {
		b, ok := seen[rows[i].Path]
		if !ok {
			b = lookup(rows[i].Path)
			seen[rows[i].Path] = b
		}
		rows[i].Branch = b
	}
	return rows
}

// Where is the folder a row works in, with its branch when known.
func (r Row) Where() string {
	if r.Branch == "" {
		return filepath.Base(r.Path)
	}
	return filepath.Base(r.Path) + "@" + r.Branch
}

// Matches reports whether a row satisfies every term of a filter. A term is
// a word found anywhere in the row, or field:word for one field: state,
// reason, agent, session, window, branch, path, name or pane.
func (r Row) Matches(terms []string) bool {
	hay := strings.ToLower(strings.Join([]string{r.Name, r.Target(), r.Path, r.State, r.Reason, r.Agent, r.Branch}, " "))
	for _, t := range terms {
		if key, val, ok := strings.Cut(t, ":"); ok {
			if field, known := r.field(key); known {
				if !strings.Contains(strings.ToLower(field), val) {
					return false
				}
				continue
			}
		}
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

func (r Row) field(key string) (string, bool) {
	switch key {
	case "state":
		return r.State, true
	case "reason":
		return r.Reason, true
	case "agent":
		if r.Agent == "" {
			return "claude", true
		}
		return r.Agent, true
	case "session":
		return r.Session, true
	case "window":
		return r.WindowName, true
	case "branch":
		return r.Branch, true
	case "path":
		return r.Path, true
	case "name":
		return r.Name, true
	case "pane":
		return r.ID, true
	}
	return "", false
}

// Filter narrows rows to those matching filter, and, with attention, to the
// ones that need the user: waiting or done.
func Filter(rows []Row, filter string, attention bool) []Row {
	terms := strings.Fields(strings.ToLower(filter))
	if len(terms) == 0 && !attention {
		return rows
	}
	var out []Row
	for _, r := range rows {
		if attention && r.State != machine.Waiting && r.State != machine.Done {
			continue
		}
		if r.Matches(terms) {
			out = append(out, r)
		}
	}
	return out
}

// Tags are the short facts shown in front of a row's name: the agent when it
// is not Claude Code, and the tool a permission is asked for.
func (r Row) Tags() string {
	var tags []string
	if r.Agent != "" && r.Agent != "claude" {
		tags = append(tags, r.Agent)
	}
	if _, tool := r.Why(); tool != "" {
		tags = append(tags, tool)
	}
	if len(tags) == 0 {
		return ""
	}
	return strings.Join(tags, " · ") + " · "
}

// Target is the tmux address shown for a row.
func (r Row) Target() string { return r.Session + ":" + r.Window + "." + r.Index }

var priority = map[string]int{machine.Waiting: 0, machine.Done: 1, machine.Running: 2, machine.Idle: 3}

// Agents keeps the panes that run a live agent the deck knows about, most
// urgent first, and within waiting and done the longest wait first. A pane whose agent died keeps its options until a view sweeps
// it, so the liveness check is what hides it.
func Agents(panes []tmux.Pane, now time.Time) []Row {
	var rows []Row
	for _, p := range panes {
		if p.State == "" || p.Sidebar != "" || !p.Alive() {
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
		// Among the agents that need the user, the one kept waiting longest
		// comes first. The others keep their place, so the list does not
		// reshuffle while they work.
		if (a.State == machine.Waiting || a.State == machine.Done) && a.Since != b.Since && a.Since > 0 && b.Since > 0 {
			return a.Since < b.Since
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

// name is the label the user gave, else the agent's session title from the
// terminal title without the status glyph Claude prefixes, else the folder.
func name(p tmux.Pane) string {
	if p.Label != "" {
		return p.Label
	}
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
	{"◎", "\x1b[32m"}, // not ○, which is idle's glyph
	{"◉", "\x1b[92m"},
}

// Pulse is the animation frame the views draw with; they advance it only
// while a running agent is on screen.
var Pulse int

// PulseGlyph is frame n of the running pulse, for places that animate on
// their own clock (the tab pulse ticks once a second).
func PulseGlyph(n int) string { return pulse[n%len(pulse)].Glyph }

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

// Bar draws a used percentage as a fixed-width bar, green to red as it fills.
func Bar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	full := int(pct/100*float64(width) + 0.5)
	color := "\x1b[32m"
	switch {
	case pct >= 80:
		color = "\x1b[31m"
	case pct >= 50:
		color = "\x1b[33m"
	}
	return color + strings.Repeat("▰", full) + "\x1b[90m" + strings.Repeat("▱", width-full) + reset
}

// Plan is the plan-usage summary; long adds bars and reset times.
func Plan(l *usage.Limits, now time.Time, long bool) string {
	if l == nil {
		return ""
	}
	part := func(name string, w *usage.Window) string {
		if w == nil {
			return ""
		}
		s := fmt.Sprintf("%s %.0f%%", name, w.UsedPercentage)
		if long {
			s = fmt.Sprintf("%s %s %.0f%%", name, Bar(w.UsedPercentage, 10), w.UsedPercentage)
			if !w.ResetsAt.IsZero() {
				s += "\x1b[90m resets " + resetsAt(w.ResetsAt.Time, now) + reset
			}
		}
		return s
	}
	var parts []string
	for _, p := range []string{part("5h", l.FiveHour), part("7d", l.SevenDay)} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	out := strings.Join(parts, "   ")
	if age := now.Sub(time.Unix(l.UpdatedAtUnix, 0)); age > 10*time.Minute {
		out += "\x1b[90m (" + Age(age) + " ago)" + reset
	}
	return out
}

func resetsAt(t, now time.Time) string {
	if t.Sub(now) < 20*time.Hour {
		return t.Local().Format("15:04")
	}
	return t.Local().Format("Mon 15:04")
}

// UsageCols is the popup's context (a bar), token, and cost columns.
func UsageCols(r Row) string {
	if r.Usage == nil {
		return "\x1b[90m" + Fit("     –", 10) + " " + Fit("", 7) + " " + Fit("", 7) + reset
	}
	ctx := "\x1b[90m" + Fit("     –", 10) + reset
	if r.Usage.ContextUsed != nil {
		ctx = Bar(*r.Usage.ContextUsed, 5) + " " + Fit(fmt.Sprintf("%3.0f%%", *r.Usage.ContextUsed), 4)
	}
	return ctx + " " + Fit(usage.Tokens(r.Usage.InputTokens+r.Usage.OutputTokens), 7) + " " +
		Fit(fmt.Sprintf("$%.2f", r.Usage.CostUSD), 7)
}

// PlanLines is the sidebar's plan block: one bar per rate-limit window, with
// the time it resets, fitted to width.
func PlanLines(l *usage.Limits, now time.Time, width int) []string {
	if l == nil {
		return nil
	}
	var out []string
	for _, p := range []struct {
		name string
		w    *usage.Window
	}{{"5h", l.FiveHour}, {"7d", l.SevenDay}} {
		if p.w == nil {
			continue
		}
		line := fmt.Sprintf("%s %s %3.0f%%", p.name, Bar(p.w.UsedPercentage, 8), p.w.UsedPercentage)
		if !p.w.ResetsAt.IsZero() {
			line += "\x1b[90m ↻" + resetsAt(p.w.ResetsAt.Time, now) + reset
		}
		out = append(out, Fit(line, width))
	}
	if len(out) > 0 && now.Sub(time.Unix(l.UpdatedAtUnix, 0)) > 10*time.Minute {
		out[len(out)-1] = Fit(strings.TrimRight(out[len(out)-1], " ")+"\x1b[90m ("+Age(now.Sub(time.Unix(l.UpdatedAtUnix, 0)))+" ago)"+reset, width)
	}
	return out
}
