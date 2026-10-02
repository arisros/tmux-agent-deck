package agent

import (
	"regexp"
	"strings"

	"github.com/arisros/ytta/internal/hook"
	"github.com/arisros/ytta/internal/machine"
	"github.com/arisros/ytta/internal/tmux"
)

// Claude is Claude Code: hooks in its settings file, screen markers for the
// endings no hook reports, and a process that renames itself to its version.
var Claude = Agent{
	Name:     "claude",
	Map:      hook.Map,
	Classify: classifyClaude,
	Evidence: lastLine,
	Detect:   tmux.IsClaude,
}

// classifyClaude reads a Claude Code screen. It only concludes from explicit
// markers; "" means no Claude screen, and callers then change nothing.
//
// The footer alone proves nothing: Claude drops "esc to interrupt" while a
// tool runs in auto mode, while the user types, and when a narrow pane cuts
// the line short. Idle therefore needs the transcript's own end marker right
// above the input box: "Interrupted" (Esc, or a denied permission) or
// "· done 4:12" (a finished turn).
func classifyClaude(screen string) string {
	lines := strings.Split(strings.TrimRight(screen, "\n "), "\n")
	var tail []string
	for i := len(lines) - 1; i >= 0 && len(tail) < 30; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			tail = append(tail, lines[i])
		}
	}
	has := func(s string) bool {
		for _, l := range tail {
			if strings.Contains(l, s) {
				return true
			}
		}
		return false
	}
	footer, below := "", tail
	for i, l := range tail {
		if i >= 8 {
			break
		}
		if idleFooter(l) {
			footer, below = l, tail[:i]
			break
		}
	}
	busyBelow := false
	for _, l := range below {
		if strings.Contains(l, "tokens") {
			busyBelow = true
		}
	}
	switch {
	case has("Do you want to proceed?"), has("Enter to select"), has("Esc to cancel"):
		return machine.ScreenDialog
	case has("esc to interrupt"), has("queued messages"), strings.Contains(footer, "esc to"), busyBelow:
		return machine.ScreenWorking
	}
	for _, l := range tail {
		// The spinner above the input box: "✽ Mustering… (4m 8s · ↓ 13.3k tokens)".
		if spinner.MatchString(l) {
			return machine.ScreenWorking
		}
	}
	box := -1
	for i, l := range tail {
		if strings.HasPrefix(strings.TrimSpace(l), "❯") {
			box = i
			break
		}
	}
	if box < 0 {
		return ""
	}
	if last := lastTranscriptLine(tail[box+1:]); strings.Contains(last, "Interrupted") || turnDone.MatchString(last) {
		return machine.ScreenIdle
	}
	return machine.ScreenNoDialog
}

// spinner matches Claude's working line, "✽ Mustering… (4m 8s · ↓ 13.3k tokens)".
var spinner = regexp.MustCompile(`^\s*\S+ \S+… \(\d`)

// turnDone matches Claude's end-of-turn line, "✻ Worked for 2s · done 2:09 AM".
var turnDone = regexp.MustCompile(`· done \d{1,2}:\d{2}`)

// lastTranscriptLine is the first line above the input box that belongs to
// the transcript: separators and right-aligned notices ("✔ Update
// installed") are skipped.
func lastTranscriptLine(above []string) string {
	for _, l := range above {
		t := strings.TrimSpace(l)
		if strings.Count(t, "─") >= 10 || len(l)-len(strings.TrimLeft(l, " ")) >= 20 {
			continue
		}
		return t
	}
	return ""
}

func idleFooter(line string) bool {
	return strings.Contains(line, "for shortcuts") || strings.Contains(line, "shift+tab to cycle") ||
		strings.Contains(line, "mode on")
}

// lastLine is the transcript line above the input box, which is what an idle
// verdict relied on.
func lastLine(screen string) string {
	lines := strings.Split(strings.TrimRight(screen, "\n "), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); strings.HasPrefix(t, "❯") && i > 0 {
			return lastTranscriptLine(reverse(lines[:i]))
		}
	}
	return ""
}

func reverse(in []string) []string {
	out := make([]string, 0, len(in))
	for i := len(in) - 1; i >= 0; i-- {
		if strings.TrimSpace(in[i]) != "" {
			out = append(out, in[i])
		}
	}
	return out
}
