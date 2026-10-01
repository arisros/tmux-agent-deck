package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/arisros/tmux-agent-deck/internal/install"
	"github.com/arisros/tmux-agent-deck/internal/store"
	"github.com/arisros/tmux-agent-deck/internal/tmux"
	"github.com/arisros/tmux-agent-deck/internal/ui"
)

func runDoctor(_ []string) error {
	ok := true
	check := func(pass bool, what, detail string) {
		mark := "✔"
		if !pass {
			mark, ok = "✘", false
		}
		fmt.Printf("%s %s", mark, what)
		if detail != "" {
			fmt.Printf(": %s", detail)
		}
		fmt.Println()
	}

	bin, _ := os.Executable()
	fmt.Printf("deck %s at %s\n\n", buildVersion(), bin)
	if rev, err := os.ReadFile(filepath.Join(filepath.Dir(bin), ".rev")); err == nil {
		want := strings.TrimSpace(string(rev))
		check(want == buildVersion() || want == "", "binary matches its build stamp", "bin/.rev says "+want)
	}

	c := tmux.FromEnv()
	// The server's version, not the tmux on PATH: they can differ.
	out, err := c.Run("display-message", "-p", "#{version}")
	v := "tmux " + strings.TrimSpace(out)
	check(err == nil && tmuxAtLeast(v, 3, 3), "tmux 3.3 or newer (server)", v)
	fe, _ := c.Run("show-options", "-gv", "focus-events")
	check(strings.TrimSpace(fe) == "on", "focus-events on", "needed to repair Esc and denied prompts when you leave a pane")
	icon, _ := c.Run("show-options", "-gqv", "@deck_pane_icon")
	check(strings.TrimSpace(icon) != "", "tmux-init has run", "icons, keys and tmux hooks")
	hooks, _ := c.Run("show-hooks", "-g")
	check(strings.Contains(hooks, " focus "), "focus hooks set", "")
	keys, _ := c.Run("list-keys", "-T", "prefix")
	check(strings.Contains(keys, " popup") && strings.Contains(keys, "sidebar toggle"), "keys bound",
		"the popup and sidebar keys; another plugin or a later bind-key may have taken them")
	if sound, _ := c.Run("show-options", "-gqv", "@deck-sound"); strings.TrimSpace(sound) != "off" {
		player, _ := c.Run("show-options", "-gqv", "@deck-sound-command")
		if f := strings.Fields(player); len(f) > 0 {
			_, err := exec.LookPath(f[0])
			check(err == nil, "sound player", f[0]+"; set @deck-sound off or @deck-sound-command if it is missing")
		}
	}

	settings, err := os.ReadFile(defaultSettingsPath())
	if err != nil {
		check(false, "Claude settings readable", err.Error())
	} else {
		events, _ := install.Owned(settings)
		recording := strings.Contains(string(settings), "hook --record")
		missing := []string{}
		for _, e := range liveEvents {
			if !contains(events, e) {
				missing = append(missing, e)
			}
		}
		switch {
		case recording:
			check(false, "Claude hooks", "recorder installed; run deck install --claude --apply for the live hooks")
		case len(missing) > 0:
			check(false, "Claude hooks", "missing "+strings.Join(missing, ", ")+"; run deck install --claude --apply")
		default:
			check(true, "Claude hooks", strconv.Itoa(len(events))+" events")
		}
		var parsed struct {
			StatusLine *struct {
				Command string `json:"command"`
			} `json:"statusLine"`
		}
		_ = json.Unmarshal(settings, &parsed)
		switch {
		case parsed.StatusLine != nil && strings.Contains(parsed.StatusLine.Command, install.Marker):
			check(true, "statusLine", "the deck records token usage and plan limits")
		case parsed.StatusLine != nil:
			check(true, "statusLine", "yours is kept, so token usage and plan limits are not shown")
		default:
			check(false, "statusLine", "not set; deck install --claude --apply adds it for usage and plan limits")
		}
		if strings.Contains(string(settings), "window-status-style") {
			check(false, "no old tab coloring hooks", "a hook still sets window-status-style and will fight the deck's icons")
		}
	}

	dir := store.DefaultDir()
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	check(true, "state directory", fmt.Sprintf("%s (%d sessions)", dir, len(files)))

	if panes, err := c.ListPanes(); err == nil {
		rows := ui.Agents(panes, time.Now())
		check(true, "agents visible", fmt.Sprintf("%d (%s)", len(rows), ui.Summary(ui.Counts(rows), false)))
		stale, here := 0, map[string]bool{}
		for _, p := range panes {
			here[p.SID] = p.SID != ""
			if p.State != "" && p.Sidebar == "" && !p.Alive() {
				stale++
			}
		}
		check(stale == 0, "no stale agents", fmt.Sprintf("%d panes keep a state after their agent exited; opening a view clears them", stale))
		orphans := 0
		for _, f := range files {
			if !here[strings.TrimSuffix(filepath.Base(f), ".json")] {
				orphans++
			}
		}
		check(true, "session records", fmt.Sprintf("%d without a pane on this server; they are pruned after 72 hours", orphans))
	}
	if !ok {
		return fmt.Errorf("some checks failed")
	}
	return nil
}

// tmuxVersion finds "3.5" in "tmux 3.5a", "tmux next-3.6" or "3.3".
var tmuxVersion = regexp.MustCompile(`(\d+)\.(\d+)`)

func tmuxAtLeast(v string, major, minor int) bool {
	m := tmuxVersion.FindStringSubmatch(v)
	if m == nil {
		return false
	}
	maj, _ := strconv.Atoi(m[1])
	mnr, _ := strconv.Atoi(m[2])
	return maj > major || (maj == major && mnr >= minor)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
