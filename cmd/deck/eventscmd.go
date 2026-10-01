package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/arisros/tmux-agent-deck/internal/events"
	"github.com/arisros/tmux-agent-deck/internal/store"
)

func runEvents(args []string) error {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	pane := fs.String("pane", "", "only this pane, such as %12")
	n := fs.Int("n", 50, "print the last n events, 0 for all")
	asJSON := fs.Bool("json", false, "print JSON lines")
	if err := fs.Parse(args); err != nil {
		return err
	}
	all, err := events.Read(store.Root())
	if err != nil {
		return err
	}
	var out []events.Event
	for _, e := range all {
		if *pane == "" || e.Pane == *pane {
			out = append(out, e)
		}
	}
	if *n > 0 && len(out) > *n {
		out = out[len(out)-*n:]
	}
	if *asJSON {
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

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
