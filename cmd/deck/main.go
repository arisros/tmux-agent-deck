// Command deck is the tmux-agent-deck binary: Claude Code hooks call it, and
// tmux key bindings and hooks open its views.
package main

import (
	"fmt"
	"os"
	"time"
)

var version = "dev"

const usage = `usage: deck <command> [flags]

hooks (called by Claude Code and tmux):
  hook [--record]                      apply a hook event read from stdin (--record: only log it, redacted)
  focus <pane>                         the user looked at pane: a done agent becomes idle
  reconcile <pane>                     correct a running or waiting agent from its screen

views:
  popup [--client name]                agents of every session; enter jumps
  sidebar toggle --session S --window W
  sidebar follow --session S --window W
  list [--json]                        print the agents

setup:
  tmux-init                            bind keys, set tmux hooks and formats (run by the tpm entrypoint)
  install --claude [--record] [--apply]  add the hooks to Claude settings (preview by default)
  uninstall --claude [--apply]         remove every deck hook from Claude settings
  doctor                               check the installation
  describe                             print the state machine as Mermaid
  tick                                 print the tab pulse frame (used by @deck-tab-pulse)
  version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "hook":
		err = runHook(args, os.Stdin)
	case "focus":
		err = runFocus(args)
	case "reconcile":
		err = runReconcile(args)
	case "popup":
		err = runPopup(args)
	case "sidebar":
		err = runSidebar(args)
	case "list":
		err = runList(args)
	case "tmux-init":
		err = runTmuxInit(args)
	case "install":
		err = runInstall(args, true)
	case "uninstall":
		err = runInstall(args, false)
	case "doctor":
		err = runDoctor(args)
	case "describe":
		err = runDescribe()
	case "tick":
		// The tab pulse: tmux re-runs this every second while an agent runs.
		if time.Now().Unix()%2 == 0 {
			fmt.Print("●")
		} else {
			fmt.Print("○")
		}
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "deck:", err)
		os.Exit(1)
	}
}
