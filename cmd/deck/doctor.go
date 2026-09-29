package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	fmt.Printf("deck %s at %s\n\n", version, bin)

	out, err := exec.Command("tmux", "-V").Output()
	v := strings.TrimSpace(string(out))
	check(err == nil && tmuxAtLeast(v, 3, 2), "tmux 3.2 or newer", v)

	c := tmux.FromEnv()
	icon, _ := c.Run("show-options", "-gqv", "@deck_pane_icon")
	check(strings.TrimSpace(icon) != "", "tmux-init has run", "icons, keys and tmux hooks")
	hooks, _ := c.Run("show-hooks", "-g")
	check(strings.Contains(hooks, " focus "), "focus hooks set", "")

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
		switch {
		case strings.Contains(string(settings), "deck") && strings.Contains(string(settings), " statusline"):
			check(true, "statusLine", "the deck records token usage and plan limits")
		case strings.Contains(string(settings), `"statusLine"`):
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
		rows := ui.Agents(panes, timeNow())
		check(true, "agents visible", fmt.Sprintf("%d (%s)", len(rows), ui.Summary(ui.Counts(rows), false)))
	}
	if !ok {
		return fmt.Errorf("some checks failed")
	}
	return nil
}

func tmuxAtLeast(v string, major, minor int) bool {
	f := strings.Fields(v)
	if len(f) < 2 {
		return false
	}
	num := strings.TrimRightFunc(f[len(f)-1], func(r rune) bool { return r < '0' || r > '9' })
	parts := strings.SplitN(num, ".", 2)
	maj, _ := strconv.Atoi(parts[0])
	mnr := 0
	if len(parts) == 2 {
		mnr, _ = strconv.Atoi(parts[1])
	}
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

var timeNow = time.Now
