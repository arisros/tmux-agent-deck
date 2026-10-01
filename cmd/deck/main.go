// Command deck is the tmux-agent-deck binary: Claude Code hooks call it, and
// tmux key bindings and hooks open its views.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"time"

	"github.com/arisros/tmux-agent-deck/internal/ui"
)

// version is set at build time (-X main.version) by the Makefile, the tpm
// entrypoint and GoReleaser; buildVersion covers `go install`.
var version = ""

func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

const usageText = `usage: deck <command> [flags]

views:
  popup                                agents of every session, most urgent first; enter jumps, x kills
  sidebar toggle --session S --window W   this session's agents in a pane that follows you
  list [--json]                        print the agents and the plan usage
  events [--pane P] [-n N] [--json]    print the log of state changes, oldest first

setup:
  install --claude [--record] [--apply] [--settings FILE]
                                       add the hooks and statusLine to Claude settings (preview by default)
  uninstall --claude [--apply] [--settings FILE]
                                       remove every deck hook and the deck's statusLine
  doctor                               check the installation
  tmux-init                            bind keys, set tmux hooks and formats (run by the tpm entrypoint)
  describe                             print the state machine as Mermaid
  version, --version, -h, --help

called by Claude Code and tmux, not by hand:
  hook [--record]                      apply a hook event read from stdin
  statusline                           record usage and plan limits, print Claude's status line
  focus <pane>                         the user looked at pane: a done agent becomes idle
  reconcile <pane>                     correct a running or waiting agent from its screen
  sidebar run --session S              the sidebar process itself
  sidebar pin --session S              put the sidebar back as the left column
  tick                                 the tab pulse frame (@deck-tab-pulse)
`

// commands is the dispatch table; the usage text is tested against it.
var commands = map[string]func(args []string) error{
	"hook":      func(a []string) error { return runHook(a, os.Stdin) },
	"focus":     runFocus,
	"reconcile": runReconcile,
	"popup":     runPopup,
	"sidebar":   runSidebar,
	"list":      runList,
	"events":    runEvents,
	"tmux-init": runTmuxInit,
	"install":   func(a []string) error { return runInstall(a, true) },
	"uninstall": func(a []string) error { return runInstall(a, false) },
	"doctor":    runDoctor,
	"describe":  func([]string) error { return runDescribe() },
	"statusline": func([]string) error {
		runStatusLine(os.Stdin)
		return nil
	},
	"tick": func([]string) error {
		// The tab pulse: tmux re-runs this once a second while an agent runs,
		// with the same frames the views animate.
		fmt.Print(ui.PulseGlyph(int(time.Now().Unix())))
		return nil
	},
	"version": func([]string) error {
		fmt.Println(buildVersion())
		return nil
	},
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usageText)
		return 0
	case "--version", "-v":
		fmt.Fprintln(stdout, buildVersion())
		return 0
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "deck: unknown command %q\n\n%s", args[0], usageText)
		return 2
	}
	if err := cmd(args[1:]); err != nil {
		fmt.Fprintln(stderr, "deck:", err)
		return 1
	}
	return 0
}
