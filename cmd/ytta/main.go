// Command ytta is the ytta binary: Claude Code hooks call it, and
// tmux key bindings and hooks open its views.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"time"

	"github.com/arisros/ytta/internal/ui"
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

const usageText = `usage: ytta <command> [flags]

views:
  popup                                agents of every session, most urgent first, with the selected one's screen;
                                       enter jumps, p sends a prompt, 1-9 answers a dialog, i interrupts, s marks seen,
                                       y copies its output, r renames it, x kills
  sidebar toggle --session S --window W   this session's agents in a pane that follows you
  list [--json] [--filter TERMS]       print the agents and the plan usage
  events [--pane P] [-n N] [--json] [--follow]
                                       print the log of state changes, oldest first; --follow keeps printing

act on an agent (only ever a pane whose agent is still running):
  send [--no-enter] <pane> [text...]   type a prompt into the agent and submit it; text from stdin when omitted
  interrupt <pane>                     stop the agent's turn, as Esc in its pane would
  rename <pane> [name...]              label the agent in the views; no name gives it back its own title
  wait [--state S,S] [--timeout D] <pane>
                                       block until the agent is waiting, done or idle (or the states given), print which

setup:
  install --claude [--record] [--wrap-statusline] [--apply] [--settings FILE]
                                       add the hooks and statusLine to Claude settings (preview by default);
                                       --wrap-statusline keeps a statusLine of your own and records usage through it
  install --codex [--record] [--apply] [--settings FILE]
                                       add the hooks to Codex's hooks.json; trust them with /hooks in Codex
  install --gemini [--record] [--apply] [--settings FILE]
                                       add the hooks to Gemini CLI's settings.json
  install --opencode [--apply] [--settings FILE]
                                       write the plugin that reports opencode's events
  uninstall --claude|--codex|--gemini|--opencode [--apply] [--settings FILE]
                                       remove every ytta hook, ytta's statusLine from Claude, or the plugin
  doctor                               check the installation
  tmux-init                            bind keys, set tmux hooks and formats (run by the tpm entrypoint)
  describe                             print the state machine as Mermaid
  version, --version, -h, --help

called by the agents and tmux, not by hand:
  hook [--agent NAME] [--record]       apply a hook event read from stdin
  statusline                           record usage and plan limits, print Claude's status line
  usage                                record the usage another agent reports about a session, read from stdin
  focus <pane>                         the user looked at pane: a done agent becomes idle
  reconcile <pane>                     correct a running or waiting agent from its screen
  sidebar run --session S              the sidebar process itself
  sidebar pin --session S              put the sidebar back as the left column
  tick                                 the tab pulse frame (@ytta-tab-pulse)
`

// commands is the dispatch table; the usage text is tested against it.
var commands = map[string]func(args []string) error{
	"hook":      func(a []string) error { return runHook(a, os.Stdin) },
	"usage":     func([]string) error { return runUsage(os.Stdin) },
	"focus":     runFocus,
	"reconcile": runReconcile,
	"popup":     runPopup,
	"sidebar":   runSidebar,
	"list":      runList,
	"events":    runEvents,
	"send":      func(a []string) error { return runSend(a, os.Stdin) },
	"interrupt": runInterrupt,
	"wait":      runWait,
	"rename":    runRename,
	"tmux-init": runTmuxInit,
	"install":   func(a []string) error { return runInstall(a, true) },
	"uninstall": func(a []string) error { return runInstall(a, false) },
	"doctor":    runDoctor,
	"describe":  func([]string) error { return runDescribe() },
	"statusline": func(a []string) error {
		runStatusLine(a, os.Stdin)
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
		fmt.Fprintf(stderr, "ytta: unknown command %q\n\n%s", args[0], usageText)
		return 2
	}
	if err := cmd(args[1:]); err != nil {
		fmt.Fprintln(stderr, "ytta:", err)
		return 1
	}
	return 0
}
