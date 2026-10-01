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
// Glyphs match the views (the running pulse comes from ui.PulseGlyph): a red badge for waiting (the one state that needs
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

// sidebarFollowCommands joins the sidebar and restores the window's saved layout, which plain
// joins drift. The swap fixes pane list order, which tmux 3.7 changed for join-pane -b.
const sidebarFollowCommands = `set-option -w -t #{window_id} @deck_restore ` +
	`"#{&&:#{@deck_layout_with},#{==:#{window_layout},#{@deck_layout_left}}}" ; ` +
	`set-option -w -F -t #{@deck_sidebar_pane} @deck_layout_with ` +
	`"##{?##{==:##{pane_index},##{pane-base-index}},##{window_layout},}" ; ` +
	`join-pane -d -f -h -b -l #{@deck-sidebar-width} -s #{@deck_sidebar_pane} -t #{window_id}.#{pane-base-index} ; ` +
	`set-option -w -F -t #{window_id} @deck_layout_joined "##{window_layout}" ; ` +
	`swap-pane -s #{@deck_sidebar_pane} -t #{window_id}.#{pane-base-index} ; ` +
	`run-shell -t #{window_id} -C 'select-layout -t #{window_id} ` +
	`"##{?##{@deck_restore},##{@deck_layout_with},##{@deck_layout_joined}}"' ; ` +
	`set-option -t #{window_id} @deck_sidebar_window #{window_id} ; ` +
	`set-option -w -F -t #{@deck_sidebar_window} @deck_layout_left "##{window_layout}"`

// sidebarMisplaced is true, in a window's context, unless its sidebar is the
// full-height left column. Edge flags, not heights: see sidebarPin.
const sidebarMisplaced = `#{!=:#{P:#{?#{==:#{pane_id},#{@deck_sidebar_pane}},#{pane_at_left}#{pane_at_top}#{pane_at_bottom},}},111}`

// hookIndex keeps the deck's tmux hooks in their own array slots, so a
// user's plain `set-hook -g name` (slot 0) and re-runs of this command never
// clobber or duplicate each other.
const hookIndex = "[77]"

func defaults() map[string]string {
	d := map[string]string{
		"@deck-popup-key":     "a",
		"@deck-sidebar-key":   "e",
		"@deck-sidebar-width": defaultSidebarWidth,
		"@deck-sound":         "on",
		"@deck-tab-pulse":     "off",
		"@deck-sidebar-pin":   "on",
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
	if v, err := c.Run("display-message", "-p", "#{version}"); err == nil && !tmuxAtLeast("tmux "+strings.TrimSpace(v), 3, 3) {
		// display-popup -b and -T, which the popup binding uses, arrived in 3.3.
		return fmt.Errorf("tmux-agent-deck needs tmux 3.3 or newer, this server is %s", strings.TrimSpace(v))
	}

	var cmds [][]string
	// The focus hooks, including the repair of silent endings when you leave
	// a pane, only fire with focus events on.
	if out, _ := c.Run("show-options", "-gv", "focus-events"); strings.TrimSpace(out) != "on" {
		cmds = append(cmds, []string{"set-option", "-g", "focus-events", "on"})
	}
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
	saved, _ := c.Run("show-options", "-gqv", "@deck-saved-status-interval")
	saved = strings.TrimSpace(saved)
	if values["@deck-tab-pulse"] == "on" {
		running = fmt.Sprintf(runningPulseIcon, bin)
		// A second-by-second pulse needs a second-by-second redraw. The
		// user's own interval is kept, to be restored when pulse goes off.
		if saved == "" {
			if cur, err := c.Run("show-options", "-gv", "status-interval"); err == nil && strings.TrimSpace(cur) != "1" {
				cmds = append(cmds, []string{"set-option", "-g", "@deck-saved-status-interval", strings.TrimSpace(cur)})
			}
		}
		cmds = append(cmds, []string{"set-option", "-g", "status-interval", "1"})
	} else if saved != "" {
		cmds = append(cmds,
			[]string{"set-option", "-g", "status-interval", saved},
			[]string{"set-option", "-gu", "@deck-saved-status-interval"})
	}
	cmds = append(cmds,
		[]string{"set-option", "-g", "@deck_is_claude", tmux.IsClaudeFormat},
		[]string{"set-option", "-g", "@deck_pane_icon", paneIconFormat(running)},
		[]string{"set-option", "-g", "@deck_window_icon", windowIconFormat(running)},
		[]string{"bind-key", values["@deck-popup-key"], "display-popup", "-E", "-w", "90%", "-h", "70%", "-b", "rounded",
			"-T", " agents ", bin + " popup"},
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
	// Runs inside tmux, no process per switch. See sidebarFollowCommands.
	cmds = append(cmds,
		[]string{"set-option", "-g", "@deck_follow", sidebarFollowCommands},
		[]string{"set-hook", "-g", "session-window-changed[78]",
			`if-shell -F "#{&&:#{@deck_sidebar_pane},#{!=:#{@deck_sidebar_window},#{window_id}}}" ` +
				`{ run-shell -C "#{E:@deck_follow}" }`})
	// Swaps, rotations and layout changes move the sidebar like any pane;
	// it pins itself back. The condition runs in tmux, so only a misplaced
	// sidebar in its own window starts anything.
	pin := `if-shell -F "#{&&:#{@deck_sidebar_pane},#{&&:#{==:#{@deck_sidebar_window},#{window_id}},` + sidebarMisplaced + `}}" ` +
		`"run-shell -b '` + bin + ` sidebar pin --session #{q:session_id}'"`
	// window-layout-changed covers them all: tmux has no after- hook for
	// swap-pane, rotate-window or join-pane. The pin itself changes the
	// layout once more, and then finds the sidebar in place.
	if values["@deck-sidebar-pin"] != "off" {
		cmds = append(cmds, []string{"set-hook", "-g", "window-layout-changed" + hookIndex, pin})
	} else {
		cmds = append(cmds, []string{"set-hook", "-gu", "window-layout-changed" + hookIndex})
	}
	// Any focus change wakes open sidebars, so the "you are here" mark moves
	// at once. wait-for runs inside tmux: no process is started.
	for _, h := range []string{"after-select-pane", "session-window-changed", "client-session-changed"} {
		cmds = append(cmds, []string{"set-hook", "-g", h + "[79]", "wait-for -S " + deck.Signal})
	}
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
