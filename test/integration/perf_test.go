package integration

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// budget reads a millisecond budget, overridable for slower CI machines.
func budget(name string, def int) time.Duration {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return time.Duration(v) * time.Millisecond
	}
	return time.Duration(def) * time.Millisecond
}

func percentile(d []time.Duration, p float64) time.Duration {
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[int(float64(len(s)-1)*p)]
}

// TestPerformanceAtScale loads a server the size of a real working day, the
// one that broke other plugins: 11 sessions, 64 windows, 120 panes, 9 agents.
func TestPerformanceAtScale(t *testing.T) {
	// Timing only means something without other test packages competing for
	// the CPU, so it runs on its own: make perf, or DECK_PERF=1.
	if os.Getenv("DECK_PERF") != "1" {
		t.Skip("set DECK_PERF=1 (make perf) to run the performance test on its own")
	}
	h := newHarness(t)

	const sessions, windows, panes, agentCount = 11, 64, 120, 9
	names := []string{"alpha"}
	for i := 1; i < sessions; i++ {
		n := fmt.Sprintf("s%02d", i)
		h.tmux("new-session", "-d", "-s", n, "-x", "240", "-y", "70", "sleep 100000")
		names = append(names, n)
	}
	for w := sessions; w < windows; w++ {
		h.tmux("new-window", "-d", "-t", names[w%sessions]+":", "sleep 100000")
	}
	wins := strings.Split(h.tmux("list-windows", "-a", "-F", "#{window_id}"), "\n")
	for p := windows; p < panes-agentCount; p++ {
		h.tmux("split-window", "-d", "-t", wins[p%len(wins)], "sleep 100000")
	}
	var agents []string
	for i := 0; i < agentCount; i++ {
		agents = append(agents, h.agent(wins[(i*7)%len(wins)]))
	}
	if got := len(strings.Split(h.tmux("list-panes", "-a", "-F", "#{pane_id}"), "\n")); got != panes {
		t.Fatalf("built %d panes, want %d", got, panes)
	}
	for _, a := range agents {
		h.hook(a, "SessionStart", `,"source":"startup"`)
		h.hook(a, "UserPromptSubmit", "")
	}

	// Hot path: tool calls inside a running turn.
	var hot []time.Duration
	for i := 0; i < 150; i++ {
		a := agents[i%len(agents)]
		hot = append(hot, h.hook(a, "PreToolUse", ""), h.hook(a, "PostToolUse", ""))
	}
	// Transitions: each publishes to tmux; Stop also asks for visibility.
	var edges []time.Duration
	for i := 0; i < 40; i++ {
		a := agents[i%len(agents)]
		edges = append(edges,
			h.hook(a, "PermissionRequest", ""),
			h.hook(a, "PostToolUse", ""),
			h.hook(a, "Stop", `,"background_tasks":[]`),
			h.hook(a, "UserPromptSubmit", ""))
	}
	// What the popup and sidebar pay per refresh.
	var views []time.Duration
	for i := 0; i < 20; i++ {
		start := time.Now()
		h.deck("", "list")
		views = append(views, time.Since(start))
	}

	report := func(name string, d []time.Duration, limit time.Duration) {
		p50, p95 := percentile(d, 0.5), percentile(d, 0.95)
		t.Logf("%-28s n=%-4d p50=%-8s p95=%-8s budget p95<%s", name, len(d), p50.Round(100*time.Microsecond), p95.Round(100*time.Microsecond), limit)
		if p95 > limit {
			t.Errorf("%s p95 %s over budget %s", name, p95, limit)
		}
	}
	report("hook, no state change", hot, budget("DECK_PERF_HOOK_MS", 15))
	report("hook, state change", edges, budget("DECK_PERF_EDGE_MS", 25))
	report("list (view refresh)", views, budget("DECK_PERF_VIEW_MS", 80))

	// Nothing may keep running between events: no daemon, no ticker.
	if out, _ := exec.Command("pgrep", "-f", h.bin).Output(); len(strings.TrimSpace(string(out))) > 0 {
		t.Errorf("deck processes alive at idle: %s", out)
	}
}
