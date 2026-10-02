package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/arisros/ytta/internal/store"
	"github.com/arisros/ytta/internal/tmux"
	"github.com/arisros/ytta/internal/ytta"
)

// Formats users embed in their own status line and borders:
//
//	window-status-format: ... #{E:@ytta_window_icon}
//	pane-border-format:   ... #{E:@ytta_pane_icon}
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
	// With @ytta-tab-pulse on, the dot comes from `ytta tick`, which the
	// status line re-runs every second, since tmux formats have no clock.
	runningPulseIcon = ` #[fg=brightgreen#,bold]#(%s tick)#[default]`
	idleIcon         = ` #[fg=colour244]○#[default]`

	// Every pane of the window contributes its state; the most urgent wins.
	windowStates = `#{P:#{?#{E:@ytta_alive},#{@ytta_state},} }`
)

func paneIconFormat(running string) string {
	return `#{?#{E:@ytta_alive},#{?#{==:#{@ytta_state},waiting},` + waitingIcon +
		`,#{?#{==:#{@ytta_state},done},` + doneIcon +
		`,#{?#{==:#{@ytta_state},running},` + running +
		`,#{?#{==:#{@ytta_state},idle},` + idleIcon + `,}}}},}`
}

func windowIconFormat(running string) string {
	return `#{?#{m:*waiting*,` + windowStates + `},` + waitingIcon +
		`,#{?#{m:*done*,` + windowStates + `},` + doneIcon +
		`,#{?#{m:*running*,` + windowStates + `},` + running + `,}}}`
}

// sidebarFollowCommands moves the sidebar into the window the session now
// shows. It is stored in @ytta_follow and run with run-shell -C, which
// expands it once in the new window before any command runs:
// #{@ytta_sidebar_window} is still the window being left, and ##-escaped
// formats are expanded later, by set-option -F or the inner run-shell.
//
// tmux hands a leaving pane's columns to its neighbour but takes a joining
// full-height pane's columns from the far edge, so plain joins shift width
// from the rightmost pane to the leftmost one on every switch. Each window
// keeps the layout it had with the sidebar (@ytta_layout_with) and the one
// the sidebar left behind (@ytta_layout_left); the first is put back while
// the window still has the second, so a resize or new pane in between wins.
//
// select-layout hands out cells in pane list order, and where join-pane -b
// puts the pane in that list changed in tmux 3.7. The swap makes the sidebar
// first on every version, and the layout applied after it puts every pane
// back in its cell: the saved one, or the one the join just produced.
const sidebarFollowCommands = `set-option -w -t #{window_id} @ytta_restore ` +
	`"#{&&:#{@ytta_layout_with},#{==:#{window_layout},#{@ytta_layout_left}}}" ; ` +
	`set-option -w -F -t #{@ytta_sidebar_pane} @ytta_layout_with ` +
	`"##{?##{==:##{pane_index},##{pane-base-index}},##{window_layout},}" ; ` +
	`join-pane -d -f -h -b -l #{@ytta-sidebar-width} -s #{@ytta_sidebar_pane} -t #{window_id}.#{pane-base-index} ; ` +
	`set-option -w -F -t #{window_id} @ytta_layout_joined "##{window_layout}" ; ` +
	`swap-pane -s #{@ytta_sidebar_pane} -t #{window_id}.#{pane-base-index} ; ` +
	`run-shell -t #{window_id} -C 'select-layout -t #{window_id} ` +
	`"##{?##{@ytta_restore},##{@ytta_layout_with},##{@ytta_layout_joined}}"' ; ` +
	`set-option -t #{window_id} @ytta_sidebar_window #{window_id} ; ` +
	`set-option -w -F -t #{@ytta_sidebar_window} @ytta_layout_left "##{window_layout}"`

// sidebarMisplaced is true, in a window's context, unless its sidebar is the
// full-height left column. Edge flags, not heights: see sidebarPin.
const sidebarMisplaced = `#{!=:#{P:#{?#{==:#{pane_id},#{@ytta_sidebar_pane}},#{pane_at_left}#{pane_at_top}#{pane_at_bottom},}},111}`

// hookIndex keeps ytta's tmux hooks in their own array slots, so a
// user's plain `set-hook -g name` (slot 0) and re-runs of this command never
// clobber or duplicate each other.
const hookIndex = "[77]"

func defaults() map[string]string {
	d := map[string]string{
		"@ytta-popup-key":       "a",
		"@ytta-sidebar-key":     "e",
		"@ytta-sidebar-width":   defaultSidebarWidth,
		"@ytta-sound":           "on",
		"@ytta-tab-pulse":       "off",
		"@ytta-sidebar-pin":     "on",
		"@ytta-popup-attention": "off",
	}
	if runtime.GOOS == "darwin" {
		d["@ytta-sound-command"] = "afplay"
		d["@ytta-sound-done"] = "/System/Library/Sounds/Funk.aiff"
		d["@ytta-sound-waiting"] = "/System/Library/Sounds/Ping.aiff"
	} else {
		d["@ytta-sound-command"] = "paplay"
		d["@ytta-sound-done"] = "/usr/share/sounds/freedesktop/stereo/complete.oga"
		d["@ytta-sound-waiting"] = "/usr/share/sounds/freedesktop/stereo/bell.oga"
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
		return errors.New("ytta must be installed under a path without spaces or quotes: " + bin)
	}
	c := tmux.FromEnv()
	version, verr := c.Run("display-message", "-p", "#{version}")
	version = "tmux " + strings.TrimSpace(version)
	if verr == nil && !tmuxAtLeast(version, 3, 2) {
		// display-popup and run-shell -C, which the sidebar follow uses, arrived in 3.2.
		return fmt.Errorf("ytta needs tmux 3.2 or newer, this server is %s", version)
	}
	popup := []string{"display-popup", "-E", "-w", "90%", "-h", "70%"}
	if verr != nil || tmuxAtLeast(version, 3, 3) {
		// The border style and the title arrived in 3.3; 3.2 gets a plain popup.
		popup = append(popup, "-b", "rounded", "-T", " agents ")
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
	saved, _ := c.Run("show-options", "-gqv", "@ytta-saved-status-interval")
	saved = strings.TrimSpace(saved)
	if values["@ytta-tab-pulse"] == "on" {
		running = fmt.Sprintf(runningPulseIcon, bin)
		// A second-by-second pulse needs a second-by-second redraw. The
		// user's own interval is kept, to be restored when pulse goes off.
		if saved == "" {
			if cur, err := c.Run("show-options", "-gv", "status-interval"); err == nil && strings.TrimSpace(cur) != "1" {
				cmds = append(cmds, []string{"set-option", "-g", "@ytta-saved-status-interval", strings.TrimSpace(cur)})
			}
		}
		cmds = append(cmds, []string{"set-option", "-g", "status-interval", "1"})
	} else if saved != "" {
		cmds = append(cmds,
			[]string{"set-option", "-g", "status-interval", saved},
			[]string{"set-option", "-gu", "@ytta-saved-status-interval"})
	}
	cmds = append(cmds,
		[]string{"set-option", "-g", "@ytta_alive", tmux.AliveFormat},
		[]string{"set-option", "-gu", "@ytta_is_claude"},
		[]string{"set-option", "-g", "@ytta_pane_icon", paneIconFormat(running)},
		[]string{"set-option", "-g", "@ytta_window_icon", windowIconFormat(running)},
		append(append([]string{"bind-key", values["@ytta-popup-key"]}, popup...), bin+" popup"),
		[]string{"bind-key", values["@ytta-sidebar-key"], "run-shell", "-b",
			bin + " sidebar toggle --session #{q:session_id} --window #{q:window_id}"},
	)

	// Looking at a done agent makes it idle. The condition runs inside tmux,
	// so switching panes spawns nothing unless the pane really is done.
	focus := `if-shell -F "#{==:#{@ytta_state},done}" "run-shell -b '` + bin + ` focus #{pane_id}'"`
	for _, h := range []string{"pane-focus-in", "after-select-pane", "session-window-changed", "client-session-changed"} {
		cmds = append(cmds, []string{"set-hook", "-g", h + hookIndex, focus})
	}
	// Leaving a busy agent is when a silent Esc or denial just happened there.
	cmds = append(cmds, []string{"set-hook", "-g", "pane-focus-out" + hookIndex,
		`if-shell -F "#{||:#{==:#{@ytta_state},running},#{==:#{@ytta_state},waiting}}" "run-shell -b '` + bin + ` reconcile #{pane_id}'"`})
	// The sidebar follows window switches entirely inside tmux: run-shell -C
	// expands the formats and runs the result as one tmux command list, so a
	// switch starts no process, and the join and the layout restore land in
	// the same redraw. See sidebarFollowCommands.
	cmds = append(cmds,
		[]string{"set-option", "-g", "@ytta_follow", sidebarFollowCommands},
		[]string{"set-hook", "-g", "session-window-changed[78]",
			`if-shell -F "#{&&:#{@ytta_sidebar_pane},#{!=:#{@ytta_sidebar_window},#{window_id}}}" ` +
				`{ run-shell -C "#{E:@ytta_follow}" }`})
	// Swaps, rotations and layout changes move the sidebar like any pane;
	// it pins itself back. The condition runs in tmux, so only a misplaced
	// sidebar in its own window starts anything.
	pin := `if-shell -F "#{&&:#{@ytta_sidebar_pane},#{&&:#{==:#{@ytta_sidebar_window},#{window_id}},` + sidebarMisplaced + `}}" ` +
		`"run-shell -b '` + bin + ` sidebar pin --session #{q:session_id}'"`
	// window-layout-changed covers them all: tmux has no after- hook for
	// swap-pane, rotate-window or join-pane. The pin itself changes the
	// layout once more, and then finds the sidebar in place.
	if values["@ytta-sidebar-pin"] != "off" {
		cmds = append(cmds, []string{"set-hook", "-g", "window-layout-changed" + hookIndex, pin})
	} else {
		cmds = append(cmds, []string{"set-hook", "-gu", "window-layout-changed" + hookIndex})
	}
	// Any focus change wakes open sidebars, so the "you are here" mark moves
	// at once. wait-for runs inside tmux: no process is started.
	for _, h := range []string{"after-select-pane", "session-window-changed", "client-session-changed"} {
		cmds = append(cmds, []string{"set-hook", "-g", h + "[79]", "wait-for -S " + ytta.Signal})
	}
	if err := c.Batch(cmds); err != nil {
		return err
	}
	// Agents already running when the plugin loads show up at once, not only
	// after their next hook.
	if panes, err := c.ListPanes(); err == nil {
		if d, err := ytta.New(c, store.DefaultDir()); err == nil {
			d.Discover(panes)
		}
	}
	return nil
}
