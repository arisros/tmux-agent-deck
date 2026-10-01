package tmux

import (
	"strings"
	"testing"
)

func TestChunkStaysUnderLimit(t *testing.T) {
	var cmds [][]string
	for i := 0; i < 400; i++ {
		cmds = append(cmds, []string{"set-option", "-p", "-t", "%123", "@deck_state", "running"})
	}
	chunks := Chunk(cmds, MaxBatchBytes)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks for %d commands", len(chunks), len(cmds))
	}
	total := 0
	for _, c := range chunks {
		size := 0
		for _, a := range c {
			size += len(a) + 1
			if a != ";" {
				continue
			}
			total++
		}
		if size > MaxBatchBytes {
			t.Errorf("chunk of %d bytes", size)
		}
		if c[0] == ";" || c[len(c)-1] == ";" {
			t.Error("chunk starts or ends with a separator")
		}
	}
	if total+len(chunks) != len(cmds) {
		t.Errorf("lost commands: %d separators + %d chunks != %d", total, len(chunks), len(cmds))
	}
}

func TestChunkSmallBatchIsOneCall(t *testing.T) {
	got := Chunk([][]string{{"a", "b"}, {"c"}}, MaxBatchBytes)
	if len(got) != 1 || strings.Join(got[0], " ") != "a b ; c" {
		t.Errorf("got %q", got)
	}
}

func TestParsePanes(t *testing.T) {
	line := strings.Join([]string{"%5", "work", "$1", "3", "@7", "2", "api", "2.1.284", "✳ Fix it", "/tmp",
		"waiting", "s1", "", "1700", "1", "0", "2", "3", "permission Bash", "2.1.284", "claude", "1650", "billing"}, sep)
	// The last fields are usually empty, so the line ends in tabs.
	quiet := strings.Join([]string{"%6", "work", "$1", "3", "@7", "3", "api", "2.1.284", "", "/tmp",
		"running", "s2", "", "1701", "0", "0", "2", "3", "", "", "", "", ""}, sep)
	got := ParsePanes(line + "\n" + "garbage\n" + quiet + "\n")
	if len(got) != 2 {
		t.Fatalf("got %d panes", len(got))
	}
	p := got[0]
	if p.ID != "%5" || p.State != "waiting" || p.Since != 1700 || !p.PaneActive || p.WindowActive || !p.Attached || p.Title != "✳ Fix it" || p.WindowPanes != 3 || p.Reason != "permission Bash" || p.Cmd != "2.1.284" || p.Agent != "claude" || p.Started != 1650 || p.Label != "billing" {
		t.Errorf("got %+v", p)
	}
	if got[1].ID != "%6" || got[1].Reason != "" {
		t.Errorf("got %+v", got[1])
	}
}

func TestAlive(t *testing.T) {
	cases := []struct {
		name         string
		command, cmd string
		want         bool
	}{
		{"claude by name, before any hook remembered it", "2.1.284", "", true},
		{"claude renamed itself after the hook", "2.1.284", "claude", true},
		{"an agent behind a wrapper", "node", "node", true},
		{"the wrapper exited to a shell", "zsh", "node", false},
		{"claude exited to a shell", "zsh", "2.1.284", false},
		{"a shell nobody remembered", "zsh", "", false},
	}
	for _, c := range cases {
		if got := (Pane{Command: c.command, Cmd: c.cmd}).Alive(); got != c.want {
			t.Errorf("%s: Alive = %v", c.name, got)
		}
	}
}

func TestIsClaude(t *testing.T) {
	for cmd, want := range map[string]bool{
		"2.1.284": true, "claude": true, "claude-code": true,
		"zsh": false, "nvim": false, "1.2": false, "1.2.x": false, "1..3": false,
	} {
		if got := IsClaude(cmd); got != want {
			t.Errorf("IsClaude(%q) = %v", cmd, got)
		}
	}
}
