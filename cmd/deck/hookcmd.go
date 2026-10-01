package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/arisros/fate/render"

	"github.com/arisros/tmux-agent-deck/internal/deck"
	"github.com/arisros/tmux-agent-deck/internal/events"
	"github.com/arisros/tmux-agent-deck/internal/hook"
	"github.com/arisros/tmux-agent-deck/internal/machine"
	"github.com/arisros/tmux-agent-deck/internal/record"
	"github.com/arisros/tmux-agent-deck/internal/store"
	"github.com/arisros/tmux-agent-deck/internal/tmux"
	"github.com/arisros/tmux-agent-deck/internal/usage"
)

func newDeck() (*deck.Deck, tmux.Client, error) {
	c := tmux.FromEnv()
	d, err := deck.New(c, store.DefaultDir())
	if d != nil {
		d.Log = func(s string) { logView(s, nil) }
		d.Emit = events.Writer(store.Root())
	}
	return d, c, err
}

// runHook must never disturb Claude: failures go to stderr and the exit
// status stays 0, because a non-zero status is surfaced to the user and, for
// some events, changes what Claude does.
func runHook(args []string, stdin io.Reader) error {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	rec := fs.Bool("record", false, "only record a redacted event")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pane := os.Getenv("TMUX_PANE")
	var err error
	if *rec {
		var e record.Entry
		if e, err = record.FromHook(stdin, pane, time.Now()); err == nil {
			err = record.Append(record.DefaultDir(), e)
		}
	} else {
		var p hook.Payload
		if p, err = hook.Decode(stdin); err == nil {
			var d *deck.Deck
			if d, _, err = newDeck(); err == nil {
				err = d.Hook(p, pane)
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "deck hook:", err)
	}
	return nil
}

func runFocus(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: deck focus <pane>")
	}
	d, _, err := newDeck()
	if err != nil {
		return err
	}
	return d.Focus(args[0])
}

func runReconcile(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: deck reconcile <pane>")
	}
	d, c, err := newDeck()
	if err != nil {
		return err
	}
	state, err := c.PaneOption(args[0], "@deck_state")
	if err != nil {
		return err
	}
	return d.Reconcile(args[0], "", state)
}

func runDescribe() error {
	m, err := machine.New()
	if err != nil {
		return err
	}
	fmt.Print(render.Mermaid(m.Describe(), render.MermaidOptions{Direction: "LR"}))
	return nil
}

// runStatusLine is Claude's statusLine command. Like a hook it must never get
// in Claude's way: on any error it prints an empty line.
func runStatusLine(stdin io.Reader) {
	in, err := usage.Parse(stdin)
	if err != nil {
		fmt.Println()
		return
	}
	_ = usage.Record(usage.DefaultDir(store.DefaultDir()), in, os.Getenv("TMUX_PANE"), time.Now())
	fmt.Println(usage.Line(in))
}
