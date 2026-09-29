package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/arisros/tmux-agent-deck/internal/deck"
	"github.com/arisros/tmux-agent-deck/internal/store"
	"github.com/arisros/tmux-agent-deck/internal/tmux"
	"github.com/arisros/tmux-agent-deck/internal/ui"
)

// Views reconcile agents that have looked busy this long before drawing, so a
// silent Esc or denial never shows as running.
const staleAfter = 2 * time.Second

// agents lists panes once, repairs stale busy agents, and lists again only
// when a repair may have changed something.
func agents(d *deck.Deck, c tmux.Client) ([]tmux.Pane, []ui.Row, error) {
	panes, err := c.ListPanes()
	if err != nil {
		return nil, nil, err
	}
	if d.Discover(panes) > 0 {
		if panes, err = c.ListPanes(); err != nil {
			return nil, nil, err
		}
	}
	now := time.Now()
	var stale []tmux.Pane
	for _, r := range ui.Agents(panes, now) {
		if (r.State == "running" || r.State == "waiting") && r.Age >= staleAfter {
			stale = append(stale, r.Pane)
		}
	}
	if len(stale) > 0 {
		d.ReconcileStale(stale, staleAfter)
		if panes, err = c.ListPanes(); err != nil {
			return nil, nil, err
		}
	}
	return panes, ui.Agents(panes, now), nil
}

func runPopup(args []string) error {
	fs := flag.NewFlagSet("popup", flag.ContinueOnError)
	client := fs.String("client", "", "tmux client to switch")
	current := fs.String("pane", "", "pane the popup was opened from")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, c, err := newDeck()
	if err != nil {
		return err
	}
	_, rows, err := agents(d, c)
	if err != nil {
		return err
	}
	l := &ui.List{All: rows, Current: *current}

	t, err := ui.OpenTerm()
	if err != nil {
		return err
	}
	defer t.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keys, changed := t.Keys(), ui.Watch(ctx, c.Flags(), deck.Signal)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()

	for {
		w, h := t.Size()
		t.Draw(ui.Popup(l, w, h))
		select {
		case k, ok := <-keys:
			if !ok {
				logView("popup: input closed", t.Trace())
				return nil
			}
			switch l.Handle(k) {
			case ui.Quit:
				logView(fmt.Sprintf("popup: quit on %+v", k), t.Trace())
				return nil
			case ui.Jump:
				if r, ok := l.Selected(); ok {
					return jump(c, *client, r.ID, true)
				}
			case ui.Kill:
				if r, ok := l.Selected(); ok {
					_, _ = c.Run("kill-pane", "-t", r.ID)
					refresh(d, c, l)
				}
			}
		case <-changed:
			refresh(d, c, l)
		case <-tick.C:
			refresh(d, c, l)
		}
	}
}

func refresh(d *deck.Deck, c tmux.Client, l *ui.List) {
	sel, had := l.Selected()
	_, rows, err := agents(d, c)
	if err != nil {
		return
	}
	l.All = rows
	if had {
		l.Keep(sel.ID)
	}
}

func jump(c tmux.Client, client, pane string, switchClient bool) error {
	sel := [][]string{{"select-window", "-t", pane}, {"select-pane", "-t", pane}}
	if switchClient {
		sw := []string{"switch-client", "-t", pane}
		if client != "" {
			sw = []string{"switch-client", "-c", client, "-t", pane}
		}
		// Without an attached client (a script, a test) there is nothing to
		// switch, but the pane can still be made current in its session.
		if err := c.Batch(append([][]string{sw}, sel...)); err == nil {
			return nil
		}
	}
	return c.Batch(sel)
}

func runSidebar(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: deck sidebar toggle|follow|run --session S [--window W]")
	}
	fs := flag.NewFlagSet("sidebar", flag.ContinueOnError)
	session := fs.String("session", "", "tmux session id")
	window := fs.String("window", "", "tmux window id")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *session == "" {
		return errors.New("--session is required")
	}
	c := tmux.FromEnv()
	switch args[0] {
	case "toggle":
		return sidebarToggle(c, *session, *window)
	case "follow":
		return sidebarFollow(c, *session, *window)
	case "run":
		return sidebarRun(c, *session)
	}
	return fmt.Errorf("unknown sidebar command %q", args[0])
}

func sidebarPane(c tmux.Client, session string) string {
	out, err := c.Run("show-options", "-qv", "-t", session, "@deck_sidebar_pane")
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(out)
	if p == "" {
		return ""
	}
	if alive, err := c.Run("display-message", "-p", "-t", p, "#{pane_id}"); err != nil || strings.TrimSpace(alive) != p {
		_, _ = c.Run("set-option", "-u", "-t", session, "@deck_sidebar_pane")
		return ""
	}
	return p
}

func sidebarWidth(c tmux.Client) string {
	out, _ := c.Run("show-options", "-gqv", "@deck-sidebar-width")
	if w := strings.TrimSpace(out); w != "" {
		if _, err := strconv.Atoi(w); err == nil {
			return w
		}
	}
	return "34"
}

// One sidebar per session: a single pane that moves to whichever window the
// session shows, instead of a copy per window.
func sidebarToggle(c tmux.Client, session, window string) error {
	if p := sidebarPane(c, session); p != "" {
		return c.Batch([][]string{{"kill-pane", "-t", p}, {"set-option", "-u", "-t", session, "@deck_sidebar_pane"}})
	}
	if window == "" {
		return errors.New("--window is required to open the sidebar")
	}
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	out, err := c.Run("split-window", "-hbf", "-d", "-l", sidebarWidth(c), "-t", window, "-P", "-F", "#{pane_id}",
		tmuxQuote(bin)+" sidebar run --session "+tmuxQuote(session))
	if err != nil {
		return err
	}
	p := strings.TrimSpace(out)
	return c.Batch([][]string{
		{"set-option", "-p", "-t", p, "@deck_sidebar", "1"},
		{"select-pane", "-t", p, "-T", "agents"},
		{"set-option", "-t", session, "@deck_sidebar_pane", p},
	})
}

func sidebarFollow(c tmux.Client, session, window string) error {
	p := sidebarPane(c, session)
	if p == "" || window == "" {
		return nil
	}
	out, err := c.Run("display-message", "-p", "-t", p, "#{window_id}")
	if err != nil || strings.TrimSpace(out) == window {
		return err
	}
	_, err = c.Run("join-pane", "-d", "-f", "-h", "-b", "-l", sidebarWidth(c), "-s", p, "-t", window)
	return err
}

func sidebarRun(c tmux.Client, session string) error {
	d, err := deck.New(c, store.DefaultDir())
	if err != nil {
		return err
	}
	self := os.Getenv("TMUX_PANE")
	l := &ui.List{}
	var others []ui.Row
	name := session
	focused := false
	split := func(rows []ui.Row) []ui.Row {
		var mine []ui.Row
		others = others[:0]
		for _, r := range rows {
			if r.SessionID == session {
				mine = append(mine, r)
			} else {
				others = append(others, r)
			}
		}
		return mine
	}
	load := func() {
		panes, rows, err := agents(d, c)
		if err != nil {
			return
		}
		for _, p := range panes {
			if p.ID == self {
				focused = p.PaneActive && p.WindowActive && p.Attached
				name = p.Session
			}
			// The pane the user is in: the active pane of the session's
			// current window, unless that is the sidebar itself.
			if p.SessionID == session && p.WindowActive && p.PaneActive && p.ID != self {
				l.Current = p.ID
			}
		}
		sel, had := l.Selected()
		l.All = split(rows)
		if had {
			l.Keep(sel.ID)
		}
	}
	load()

	t, err := ui.OpenTerm()
	if err != nil {
		return err
	}
	defer t.Close()
	defer func() { _, _ = c.Run("set-option", "-u", "-t", session, "@deck_sidebar_pane") }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keys, changed := t.Keys(), ui.Watch(ctx, c.Flags(), deck.Signal)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()

	for {
		w, h := t.Size()
		t.Draw(ui.Sidebar(l, others, name, focused, w, h))
		select {
		case k, ok := <-keys:
			if !ok {
				return nil
			}
			switch l.Handle(k) {
			case ui.Quit:
				return nil
			case ui.Jump:
				if r, ok := l.Selected(); ok {
					_ = jump(c, "", r.ID, false)
				}
			case ui.Kill:
				if r, ok := l.Selected(); ok {
					_, _ = c.Run("kill-pane", "-t", r.ID)
				}
			}
			load()
		case <-changed:
			load()
		case <-tick.C:
			load()
		case <-winch:
		}
	}
}

func runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, c, err := newDeck()
	if err != nil {
		return err
	}
	_, rows, err := agents(d, c)
	if err != nil {
		return err
	}
	if *asJSON {
		type item struct {
			Pane, Target, State, Name, Path string
			AgeSeconds                      int64 `json:"age_seconds"`
		}
		out := []item{}
		for _, r := range rows {
			out = append(out, item{r.ID, r.Target(), r.State, r.Name, r.Path, int64(r.Age.Seconds())})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		fmt.Fprintf(tw, "%s %s\t%s\t%s\t%s\t%s\n", ui.Styles[r.State].Glyph, r.State, ui.Age(r.Age), r.Target(), r.Name, r.Path)
	}
	return tw.Flush()
}

// tmuxQuote single-quotes s for a tmux command string passed to a shell.
func tmuxQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// logView appends why a view closed, with the raw input that led there, to a
// small log next to the session records. A popup that closes on its own gives
// the user nothing to read; this does.
func logView(reason string, input []string) {
	dir := filepath.Dir(store.DefaultDir())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	name := filepath.Join(dir, "views.log")
	if fi, err := os.Stat(name); err == nil && fi.Size() > 64<<10 {
		_ = os.Remove(name)
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s input=%s\n", time.Now().Format(time.RFC3339), reason, strings.Join(input, " "))
}
