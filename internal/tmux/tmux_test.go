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
		"waiting", "s1", "", "1700", "1", "0", "2"}, sep)
	got := ParsePanes(line + "\n" + "garbage\n")
	if len(got) != 1 {
		t.Fatalf("got %d panes", len(got))
	}
	p := got[0]
	if p.ID != "%5" || p.State != "waiting" || p.Since != 1700 || !p.PaneActive || p.WindowActive || !p.Attached || p.Title != "✳ Fix it" {
		t.Errorf("got %+v", p)
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
