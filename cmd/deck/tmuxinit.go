package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/arisros/tmux-agent-deck/internal/deck"
	"github.com/arisros/tmux-agent-deck/internal/store"
	"github.com/arisros/tmux-agent-deck/internal/tmux"
)

// Formats users embed in their own status line and borders:
//
//	window-status-format: ... #{E:@deck_window_icon}
//	pane-border-format:   ... #{E:@deck_pane_icon}
//
// tmux evaluates them itself on redraw, so showing state costs no process.
// Glyphs match the views: a red badge for waiting (the one state that needs
// a decision), a green dot for running that blinks where the terminal
// supports it, blue for done, grey for idle. Blinking is a terminal
// attribute, so tmux never has to redraw to animate it.
const (
	waitingIcon = ` #[fg=brightwhite#,bg=red#,bold] ◆ #[default]`
	doneIcon    = ` #[fg=blue#,bold]✔#[default]`
	runningIcon = ` #[fg=brightgreen#,bold#,blink]●#[default]`
	// With @deck-tab-pulse on, the dot comes from `deck tick`, which the
	// status line re-runs every second, since tmux formats have no clock.
	runningPulseIcon = ` #[fg=brightgreen#,bold]#(%s tick)#[default]`
	idleIcon         = ` #[fg=colour244]○#[default]`

	// Every pane of the window contributes its state; the most urgent wins.
	windowStates = `#{P:#{?#{E:@deck_is_claude},#{@deck_state},} }`
)

func paneIconFormat(running string) string {
	return `#{?#{E:@deck_is_claude},#{?#{==:#{@deck_state},waiting},` + waitingIcon +
		`,#{?#{==:#{@deck_state},done},` + doneIcon +
		`,#{?#{==:#{@deck_state},running},` + running +
		`,#{?#{==:#{@deck_state},idle},` + idleIcon + `,}}}},}`
}

func windowIconFormat(running string) string {
	return `#{?#{m:*waiting*,` + windowStates + `},` + waitingIcon +
		`,#{?#{m:*done*,` + windowStates + `},` + doneIcon +
		`,#{?#{m:*running*,` + windowStates + `},` + running + `,}}}`
}

// hookIndex keeps the deck's tmux hooks in their own array slots, so a
// user's plain `set-hook -g name` (slot 0) and re-runs of this command never
// clobber or duplicate each other.
const hookIndex = "[77]"

func defaults() map[string]string {
	d := map[string]string{
		"@deck-popup-key":     "a",
		"@deck-sidebar-key":   "e",
		"@deck-sidebar-width": "34",
		"@deck-sound":         "on",
		"@deck-tab-pulse":     "off",
	}
	if runtime.GOOS == "darwin" {
		d["@deck-sound-command"] = "afplay"
		d["@deck-sound-done"] = "/System/Library/Sounds/Funk.aiff"
		d["@deck-sound-waiting"] = "/System/Library/Sounds/Ping.aiff"
	} else {
		d["@deck-sound-command"] = "paplay"
		d["@deck-sound-done"] = "/usr/share/sounds/freedesktop/stereo/complete.oga"
		d["@deck-sound-waiting"] = "/usr/share/sounds/freedesktop/stereo/bell.oga"
	}
	return d
}

func runTmuxInit(_ []string) error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	if bin, err = filepath.EvalSymlinks(bin); err != nil {
		return err
	}
	if strings.ContainsAny(bin, "'\"\\ ") {
		return errors.New("tmux-agent-deck must be installed under a path without spaces or quotes: " + bin)
	}
	c := tmux.FromEnv()

	var cmds [][]string
	values := map[string]string{}
	for name, def := range defaults() {
		out, _ := c.Run("show-options", "-gqv", name)
		v := strings.TrimSpace(out)
		if v == "" {
			v = def
			cmds = append(cmds, []string{"set-option", "-g", name, v})
		}
		values[name] = v
	}
	running := runningIcon
	if values["@deck-tab-pulse"] == "on" {
		running = fmt.Sprintf(runningPulseIcon, bin)
		// A second-by-second pulse needs a second-by-second redraw.
		cmds = append(cmds, []string{"set-option", "-g", "status-interval", "1"})
	}
	cmds = append(cmds,
		[]string{"set-option", "-g", "@deck_is_claude", tmux.IsClaudeFormat},
		[]string{"set-option", "-g", "@deck_pane_icon", paneIconFormat(running)},
		[]string{"set-option", "-g", "@deck_window_icon", windowIconFormat(running)},
		[]string{"bind-key", values["@deck-popup-key"], "display-popup", "-E", "-w", "90%", "-h", "70%", "-b", "rounded",
			"-T", " agents ", bin + " popup --client #{q:client_name} --pane #{pane_id}"},
		[]string{"bind-key", values["@deck-sidebar-key"], "run-shell", "-b",
			bin + " sidebar toggle --session #{q:session_id} --window #{q:window_id}"},
	)

	// Looking at a done agent makes it idle. The condition runs inside tmux,
	// so switching panes spawns nothing unless the pane really is done.
	focus := `if-shell -F "#{==:#{@deck_state},done}" "run-shell -b '` + bin + ` focus #{pane_id}'"`
	for _, h := range []string{"pane-focus-in", "after-select-pane", "session-window-changed", "client-session-changed"} {
		cmds = append(cmds, []string{"set-hook", "-g", h + hookIndex, focus})
	}
	// Leaving a busy agent is when a silent Esc or denial just happened there.
	cmds = append(cmds, []string{"set-hook", "-g", "pane-focus-out" + hookIndex,
		`if-shell -F "#{||:#{==:#{@deck_state},running},#{==:#{@deck_state},waiting}}" "run-shell -b '` + bin + ` reconcile #{pane_id}'"`})
	cmds = append(cmds, []string{"set-hook", "-g", "session-window-changed[78]",
		`if-shell -F "#{@deck_sidebar_pane}" "run-shell -b '` + bin + ` sidebar follow --session #{q:session_id} --window #{q:window_id}'"`})
	if err := c.Batch(cmds); err != nil {
		return err
	}
	// Agents already running when the plugin loads show up at once, not only
	// after their next hook.
	if panes, err := c.ListPanes(); err == nil {
		if d, err := deck.New(c, store.DefaultDir()); err == nil {
			d.Discover(panes)
		}
	}
	return nil
}
