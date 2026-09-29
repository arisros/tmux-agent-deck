// Package deck is the adapter around the pure state machine: it loads and
// saves session records, asks tmux the few questions the machine needs, and
// publishes the result as pane options that tmux formats render.
package deck

import (
	"strconv"
	"strings"
	"time"

	"github.com/arisros/tmux-agent-deck/internal/hook"
	"github.com/arisros/tmux-agent-deck/internal/machine"
	"github.com/arisros/tmux-agent-deck/internal/store"
	"github.com/arisros/tmux-agent-deck/internal/tmux"
)

// Tmux is the subset of tmux the adapter uses, so tests can fake it.
type Tmux interface {
	Visible(pane string) (bool, error)
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

	if s, ok := ev.(machine.Stop); ok && s.Background == 0 && pane != "" {
		// Only the Stop that ends a turn needs to know whether the user is
		// watching: a watched turn ends idle and silent.
		s.Visible, _ = d.Tmux.Visible(pane)
		ev = s
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
	return d.send(pane, sid, machine.Screen{At: d.Now().Unix(), Kind: kind})
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

func (d *Deck) send(pane, sid string, ev machine.Event) error {
	l, err := store.Open(d.Dir, sid)
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
	if sound != "" {
		// tmux decides and plays in the same round trip: nothing to query
		// first, and the player runs detached from the hook.
		cmds = append(cmds, []string{"if-shell", "-F", "-t", pane,
			"#{&&:#{!=:#{@deck-sound},off},#{?" + tmux.VisibleFormat + ",0,1}}",
			"run-shell -b '#{@deck-sound-command} #{@deck-sound-" + sound + "}'"})
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

// Classify reads Claude Code's footer. It answers "" when unsure, and callers
// then leave the state alone: a stale state beats a wrong one.
func Classify(screen string) string {
	lines := strings.Split(strings.TrimRight(screen, "\n "), "\n")
	var tail []string
	for i := len(lines) - 1; i >= 0 && len(tail) < 25; i-- {
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
	switch {
	case has("Do you want to proceed?"), has("Enter to select"), has("Esc to cancel"):
		return machine.ScreenDialog
	case has("esc to interrupt"):
		return machine.ScreenWorking
	case len(tail) > 0 && (strings.Contains(tail[0], "for shortcuts") ||
		strings.Contains(tail[0], "shift+tab to cycle") || strings.Contains(tail[0], "mode on")):
		return machine.ScreenIdle
	}
	return ""
}
