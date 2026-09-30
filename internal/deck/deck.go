// Package deck is the adapter around the pure state machine: it loads and
// saves session records, asks tmux the few questions the machine needs, and
// publishes the result as pane options that tmux formats render.
package deck

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/arisros/tmux-agent-deck/internal/hook"
	"github.com/arisros/tmux-agent-deck/internal/machine"
	"github.com/arisros/tmux-agent-deck/internal/store"
	"github.com/arisros/tmux-agent-deck/internal/tmux"
	"github.com/arisros/tmux-agent-deck/internal/usage"
)

// Tmux is the subset of tmux the adapter uses, so tests can fake it.
type Tmux interface {
	Batch(cmds [][]string) error
	Capture(pane string) (string, error)
	PaneOption(pane, name string) (string, error)
}

// Deck wires the machine to a store and a tmux server.
type Deck struct {
	Tmux    Tmux
	Dir     string
	Machine *machine.Machine
	Now     func() time.Time
	// Log, when set, records every state change a screen check makes: those
	// are inferences, and a wrong one should leave a trace.
	Log func(string)
}

// New returns a Deck for the given tmux server and state directory.
func New(t Tmux, dir string) (*Deck, error) {
	m, err := machine.New()
	if err != nil {
		return nil, err
	}
	return &Deck{Tmux: t, Dir: dir, Machine: m, Now: time.Now}, nil
}

// Signal is the wait-for channel sidebars block on.
const Signal = "deck"

// Hook applies one hook payload from a Claude session running in pane.
func (d *Deck) Hook(p hook.Payload, pane string) error {
	now := d.Now().Unix()
	action, ev := hook.Map(p, now)
	if action == hook.Ignore || p.SessionID == "" {
		return nil
	}
	l, err := store.Open(d.Dir, p.SessionID)
	if err != nil {
		return err
	}
	defer l.Close()
	rec, existed := l.Record()
	if pane == "" {
		pane = rec.Pane
	}

	switch action {
	case hook.End:
		if err := l.Delete(); err != nil {
			return err
		}
		if pane == "" {
			return nil
		}
		return d.Tmux.Batch(clear(pane))
	case hook.Begin:
		store.Prune(d.Dir, 72*time.Hour)
		usage.Prune(usage.DefaultDir(d.Dir), 72*time.Hour)
		var snap []byte
		if existed {
			snap = rec.Snapshot
		}
		res, err := machine.Apply(d.Machine, snap, machine.Begin{At: now})
		if err != nil {
			return err
		}
		if err := l.Save(store.Record{Pane: pane, Snapshot: res.Snapshot}); err != nil {
			return err
		}
		return d.publish(pane, p.SessionID, res, "")
	}

	var snap []byte
	if existed {
		snap = rec.Snapshot
	}
	res, err := machine.Apply(d.Machine, snap, ev)
	if err != nil {
		return err
	}
	if err := l.Save(store.Record{Pane: pane, Snapshot: res.Snapshot}); err != nil {
		return err
	}
	// Tool calls inside a running turn are the hot path and change nothing:
	// no tmux round trip at all.
	if existed && res.From == res.To && rec.Pane == pane {
		return nil
	}
	sound := ""
	switch {
	case res.Entered(machine.Waiting):
		sound = "waiting"
	case res.Entered(machine.Done) && isStop(ev):
		sound = "done"
	}
	return d.publish(pane, p.SessionID, res, sound)
}

func isStop(e machine.Event) bool { _, ok := e.(machine.Stop); return ok }

// Focus tells the machine the user looked at pane; a done agent becomes idle.
func (d *Deck) Focus(pane string) error {
	sid, err := d.Tmux.PaneOption(pane, "@deck_sid")
	if err != nil || sid == "" {
		return err
	}
	return d.send(pane, sid, machine.Focus{At: d.Now().Unix()})
}

// Reconcile corrects a running or waiting agent from what its screen shows.
// No hook fires on Esc mid-turn or on a denied permission, so without this an
// agent would look busy until its next prompt.
func (d *Deck) Reconcile(pane, sid, state string) error {
	if state != machine.Running && state != machine.Waiting {
		return nil
	}
	if sid == "" {
		var err error
		if sid, err = d.Tmux.PaneOption(pane, "@deck_sid"); err != nil || sid == "" {
			return err
		}
	}
	screen, err := d.Tmux.Capture(pane)
	if err != nil {
		return err
	}
	kind := Classify(screen)
	if kind == "" {
		return nil
	}
	from := state
	err = d.send(pane, sid, machine.Screen{At: d.Now().Unix(), Kind: kind})
	if to, _ := d.Tmux.PaneOption(pane, "@deck_state"); to != from && d.Log != nil {
		d.Log(fmt.Sprintf("reconcile %s: %s -> %s (screen %s, last line %q)", pane, from, to, kind, lastLine(screen)))
	}
	return err
}

func lastLine(screen string) string {
	lines := strings.Split(strings.TrimRight(screen, "\n "), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); strings.HasPrefix(t, "❯") && i > 0 {
			return lastTranscriptLine(reverse(lines[:i]))
		}
	}
	return ""
}

func reverse(in []string) []string {
	out := make([]string, 0, len(in))
	for i := len(in) - 1; i >= 0; i-- {
		if strings.TrimSpace(in[i]) != "" {
			out = append(out, in[i])
		}
	}
	return out
}

// ReconcileStale reconciles every agent that has been running or waiting for
// at least minAge, which is what a view does before it renders.
func (d *Deck) ReconcileStale(panes []tmux.Pane, minAge time.Duration) {
	now := d.Now().Unix()
	for _, p := range panes {
		if p.SID != "" && now-p.Since >= int64(minAge.Seconds()) {
			_ = d.Reconcile(p.ID, p.SID, p.State)
		}
	}
}

// Discover publishes a state for every Claude pane the deck has not heard
// from yet, read from its screen. An agent idle at its prompt when the hooks
// were installed fires no hook until it is used again, and would otherwise
// stay invisible. The state has no session record behind it; the session's
// next hook creates one and takes over. It returns how many panes it set.
func (d *Deck) Discover(panes []tmux.Pane) int {
	now := strconv.FormatInt(d.Now().Unix(), 10)
	var cmds [][]string
	for _, p := range panes {
		if p.State != "" || p.Sidebar != "" || !tmux.IsClaude(p.Command) {
			continue
		}
		screen, err := d.Tmux.Capture(p.ID)
		if err != nil {
			continue
		}
		state := machine.Idle // a fresh or quiet session has no end marker yet
		switch Classify(screen) {
		case machine.ScreenWorking:
			state = machine.Running
		case machine.ScreenDialog:
			state = machine.Waiting
		}
		cmds = append(cmds,
			[]string{"set-option", "-p", "-t", p.ID, "@deck_state", state},
			[]string{"set-option", "-p", "-t", p.ID, "@deck_since", now})
	}
	if len(cmds) == 0 {
		return 0
	}
	cmds = append(cmds, []string{"wait-for", "-S", Signal})
	if err := d.Tmux.Batch(cmds); err != nil {
		return 0
	}
	return (len(cmds) - 1) / 2
}

func (d *Deck) send(pane, sid string, ev machine.Event) error {
	l, err := store.OpenExisting(d.Dir, sid)
	if errors.Is(err, os.ErrNotExist) {
		return nil // pruned or never recorded: nothing to update
	}
	if err != nil {
		return err
	}
	defer l.Close()
	rec, existed := l.Record()
	if !existed {
		return nil
	}
	res, err := machine.Apply(d.Machine, rec.Snapshot, ev)
	if err != nil {
		return err
	}
	if res.From == res.To {
		return nil
	}
	if err := l.Save(store.Record{Pane: rec.Pane, Snapshot: res.Snapshot}); err != nil {
		return err
	}
	return d.publish(pane, sid, res, "")
}

func (d *Deck) publish(pane, sid string, res machine.Result, sound string) error {
	if pane == "" {
		return nil
	}
	cmds := [][]string{
		{"set-option", "-p", "-t", pane, "@deck_state", res.To},
		{"set-option", "-p", "-t", pane, "@deck_since", strconv.FormatInt(res.Ctx.Since, 10)},
		{"set-option", "-p", "-t", pane, "@deck_sid", sid},
	}
	// tmux decides and plays in the same round trip: nothing is queried
	// first, and the player runs detached from the hook.
	play := `if-shell -F "#{!=:#{@deck-sound},off}" "run-shell -b '#{@deck-sound-command} #{@deck-sound-` + sound + `}'"`
	switch {
	case sound == "done":
		// A turn the user watched end is idle and silent.
		cmds = append(cmds, []string{"if-shell", "-F", "-t", pane, tmux.VisibleFormat,
			"set-option -p -t " + pane + " @deck_state idle", play})
	case sound != "":
		cmds = append(cmds, []string{"if-shell", "-F", "-t", pane, tmux.VisibleFormat, "", play})
	}
	cmds = append(cmds, []string{"wait-for", "-S", Signal})
	return d.Tmux.Batch(cmds)
}

func clear(pane string) [][]string {
	return [][]string{
		{"set-option", "-p", "-u", "-t", pane, "@deck_state"},
		{"set-option", "-p", "-u", "-t", pane, "@deck_since"},
		{"set-option", "-p", "-u", "-t", pane, "@deck_sid"},
		{"wait-for", "-S", Signal},
	}
}

// Classify reads a Claude Code screen. It only concludes from explicit
// markers; "" means no Claude screen, and callers then change nothing.
//
// The footer alone proves nothing: Claude drops "esc to interrupt" while a
// tool runs in auto mode, while the user types, and when a narrow pane cuts
// the line short. Idle therefore needs the transcript's own end marker right
// above the input box: "Interrupted" (Esc, or a denied permission) or
// "· done 4:12" (a finished turn).
func Classify(screen string) string {
	lines := strings.Split(strings.TrimRight(screen, "\n "), "\n")
	var tail []string
	for i := len(lines) - 1; i >= 0 && len(tail) < 30; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			tail = append(tail, lines[i])
		}
	}
	has := func(s string) bool {
		for _, l := range tail {
			if strings.Contains(l, s) {
				return true
			}
		}
		return false
	}
	footer, below := "", tail
	for i, l := range tail {
		if i >= 8 {
			break
		}
		if idleFooter(l) {
			footer, below = l, tail[:i]
			break
		}
	}
	busyBelow := false
	for _, l := range below {
		if strings.Contains(l, "tokens") {
			busyBelow = true
		}
	}
	switch {
	case has("Do you want to proceed?"), has("Enter to select"), has("Esc to cancel"):
		return machine.ScreenDialog
	case has("esc to interrupt"), has("queued messages"), strings.Contains(footer, "esc to"), busyBelow:
		return machine.ScreenWorking
	}
	for _, l := range tail {
		// The spinner above the input box: "✽ Mustering… (4m 8s · ↓ 13.3k tokens)".
		if spinner.MatchString(l) {
			return machine.ScreenWorking
		}
	}
	box := -1
	for i, l := range tail {
		if strings.HasPrefix(strings.TrimSpace(l), "❯") {
			box = i
			break
		}
	}
	if box < 0 {
		return ""
	}
	if last := lastTranscriptLine(tail[box+1:]); strings.Contains(last, "Interrupted") || turnDone.MatchString(last) {
		return machine.ScreenIdle
	}
	return machine.ScreenNoDialog
}

// spinner matches Claude's working line, "✽ Mustering… (4m 8s · ↓ 13.3k tokens)".
var spinner = regexp.MustCompile(`^\s*\S+ \S+… \(\d`)

// turnDone matches Claude's end-of-turn line, "✻ Worked for 2s · done 2:09 AM".
var turnDone = regexp.MustCompile(`· done \d{1,2}:\d{2}`)

// lastTranscriptLine is the first line above the input box that belongs to
// the transcript: separators and right-aligned notices ("✔ Update
// installed") are skipped.
func lastTranscriptLine(above []string) string {
	for _, l := range above {
		t := strings.TrimSpace(l)
		if strings.Count(t, "─") >= 10 || len(l)-len(strings.TrimLeft(l, " ")) >= 20 {
			continue
		}
		return t
	}
	return ""
}

func idleFooter(line string) bool {
	return strings.Contains(line, "for shortcuts") || strings.Contains(line, "shift+tab to cycle") ||
		strings.Contains(line, "mode on")
}
