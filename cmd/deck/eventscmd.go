package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/arisros/tmux-agent-deck/internal/deck"
	"github.com/arisros/tmux-agent-deck/internal/events"
	"github.com/arisros/tmux-agent-deck/internal/store"
	"github.com/arisros/tmux-agent-deck/internal/tmux"
	"github.com/arisros/tmux-agent-deck/internal/ui"
)

func runEvents(args []string) error {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	pane := fs.String("pane", "", "only this pane, such as %12")
	n := fs.Int("n", 50, "print the last n events, 0 for all")
	asJSON := fs.Bool("json", false, "print JSON lines")
	follow := fs.Bool("follow", false, "keep printing events as they happen")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root := store.Root()
	// The offset is taken before the history is read, so an event appended in
	// between is printed twice rather than never.
	offset := events.Size(root)
	all, err := events.Read(root)
	if err != nil {
		return err
	}
	keep := func(in []events.Event) []events.Event {
		var out []events.Event
		for _, e := range in {
			if *pane == "" || e.Pane == *pane {
				out = append(out, e)
			}
		}
		return out
	}
	out := keep(all)
	if *n > 0 && len(out) > *n {
		out = out[len(out)-*n:]
	}
	if err := printEvents(out, *asJSON); err != nil || !*follow {
		return err
	}
	// Every state change ends with the signal the views block on, so this
	// sleeps inside tmux until there is something to print.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	changed := ui.Watch(ctx, tmux.FromEnv().Flags(), deck.Signal)
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-changed:
			if !ok {
				return errors.New("lost the tmux server")
			}
			var fresh []events.Event
			if fresh, offset, err = events.ReadFrom(root, offset); err != nil {
				return err
			}
			if err := printEvents(keep(fresh), *asJSON); err != nil {
				return err
			}
		}
	}
}

func printEvents(out []events.Event, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		for _, e := range out {
			if err := enc.Encode(e); err != nil {
				return err
			}
		}
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, e := range out {
		change := e.To
		if e.From != "" || e.To != "" {
			change = dash(e.From) + " -> " + dash(e.To)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			time.UnixMilli(e.TS).Format("01-02 15:04:05"), e.Pane, change, e.Kind, e.Source,
			strings.TrimSpace(e.Reason+" "+e.Tool))
	}
	return tw.Flush()
}

// runWait blocks until an agent reaches one of the wanted states, then prints
// it. It is how a script hands a prompt to an agent and picks up when the
// agent needs it again.
func runWait(args []string) error {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	states := fs.String("state", "waiting,done,idle", "comma-separated states to wait for")
	timeout := fs.Duration("timeout", 0, "give up after this long, such as 10m (default: never)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: deck wait [--state waiting,done,idle] [--timeout 10m] <pane>")
	}
	pane := fs.Arg(0)
	want := map[string]bool{}
	for _, s := range strings.Split(*states, ",") {
		switch s = strings.TrimSpace(s); s {
		case "idle", "running", "waiting", "done":
			want[s] = true
		default:
			return fmt.Errorf("unknown state %q", s)
		}
	}
	c := tmux.FromEnv()
	if err := mustBeAgent(c, pane); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	if *timeout > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, *timeout)
		defer stop()
	}
	// Subscribed before the first look, so a change in between is not missed.
	changed := ui.Watch(ctx, c.Flags(), deck.Signal)
	for {
		out, err := c.Run("display-message", "-p", "-t", pane, "#{@deck_state} "+tmux.AliveFormat)
		if err != nil {
			return fmt.Errorf("pane %s is gone", pane)
		}
		f := strings.Fields(out)
		if len(f) != 2 || f[1] != "1" {
			return fmt.Errorf("the agent in pane %s has exited", pane)
		}
		if want[f[0]] {
			fmt.Println(f[0])
			return nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("pane %s is still %s after %s", pane, f[0], *timeout)
			}
			return nil
		case _, ok := <-changed:
			if !ok {
				return errors.New("lost the tmux server")
			}
		}
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
