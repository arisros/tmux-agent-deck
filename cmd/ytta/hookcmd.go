package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/arisros/fate/render"

	"github.com/arisros/ytta/internal/events"
	"github.com/arisros/ytta/internal/hook"
	"github.com/arisros/ytta/internal/install"
	"github.com/arisros/ytta/internal/machine"
	"github.com/arisros/ytta/internal/record"
	"github.com/arisros/ytta/internal/store"
	"github.com/arisros/ytta/internal/tmux"
	"github.com/arisros/ytta/internal/usage"
	"github.com/arisros/ytta/internal/ytta"
)

func newYtta() (*ytta.Ytta, tmux.Client, error) {
	c := tmux.FromEnv()
	d, err := ytta.New(c, store.DefaultDir())
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
	name := fs.String("agent", "", "the agent sending the event (default claude)")
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
			p.Agent = *name
			var d *ytta.Ytta
			if d, _, err = newYtta(); err == nil {
				err = d.Hook(p, pane)
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ytta hook:", err)
	}
	return nil
}

// runUsage records the usage an agent reports about itself: one JSON object
// on stdin with session_id, model, cost_usd, input_tokens, output_tokens and
// context_used. Like a hook, it never fails the agent that called it.
func runUsage(stdin io.Reader) error {
	var r usage.Report
	err := json.NewDecoder(stdin).Decode(&r)
	if err == nil {
		err = usage.RecordReport(usage.DefaultDir(store.DefaultDir()), r, os.Getenv("TMUX_PANE"), time.Now())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ytta usage:", err)
	}
	return nil
}

func runFocus(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: ytta focus <pane>")
	}
	d, _, err := newYtta()
	if err != nil {
		return err
	}
	return d.Focus(args[0])
}

func runReconcile(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: ytta reconcile <pane>")
	}
	d, c, err := newYtta()
	if err != nil {
		return err
	}
	state, err := c.PaneOption(args[0], "@ytta_state")
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
// in Claude's way: on any error it prints an empty line. With --wrap64 it
// records the numbers and then prints what the user's own command prints,
// given the same input.
func runStatusLine(args []string, stdin io.Reader) {
	fs := flag.NewFlagSet("statusline", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	wrap64 := fs.String("wrap64", "", "base64 of the status line command to run after recording")
	_ = fs.Parse(args)
	raw, _ := io.ReadAll(stdin)
	in, err := usage.Parse(bytes.NewReader(raw))
	if err == nil {
		_ = usage.Record(usage.DefaultDir(store.DefaultDir()), in, os.Getenv("TMUX_PANE"), time.Now())
	}
	if original, ok := install.Unwrap(install.WrapFlag + " " + *wrap64); ok && *wrap64 != "" {
		cmd := exec.Command("sh", "-c", original)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(raw), os.Stdout, os.Stderr
		if cmd.Run() != nil {
			fmt.Println()
		}
		return
	}
	if err != nil {
		fmt.Println()
		return
	}
	fmt.Println(usage.Line(in))
}
