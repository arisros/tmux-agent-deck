package ui

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"
)

// Key is one decoded keypress.
type Key struct {
	Rune rune
	Name string // "up", "down", "enter", "esc", "backspace", "ctrl-c"; "" for a rune
}

// Term is a raw-mode terminal drawn in full on every frame.
type Term struct {
	fd    int
	state *term.State
}

// write sends a whole frame in one call, so the terminal never shows half of
// one. A failed write only costs a frame; the next redraw repeats everything.
func write(s string) { _, _ = os.Stdout.WriteString(s) }

// OpenTerm switches stdin to raw mode and takes over the screen.
func OpenTerm() (*Term, error) {
	fd := int(os.Stdin.Fd())
	st, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	write("\x1b[?1049h\x1b[?25l")
	return &Term{fd: fd, state: st}, nil
}

// Close restores the terminal.
func (t *Term) Close() {
	write("\x1b[?25h\x1b[?1049l")
	_ = term.Restore(t.fd, t.state)
}

// Size is the terminal's width and height in cells.
func (t *Term) Size() (int, int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 80, 24
	}
	return w, h
}

// Draw replaces the screen with lines, each already fitted to the width.
func (t *Term) Draw(lines []string) {
	var b strings.Builder
	b.WriteString("\x1b[H")
	for i, l := range lines {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(l)
		b.WriteString("\x1b[0m\x1b[K")
	}
	b.WriteString("\x1b[J")
	write(b.String())
}

// Keys decodes keypresses from stdin until it closes.
func (t *Term) Keys() <-chan Key {
	ch := make(chan Key)
	go func() {
		defer close(ch)
		buf := make([]byte, 64)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			for _, k := range decode(buf[:n]) {
				ch <- k
			}
		}
	}()
	return ch
}

func decode(b []byte) []Key {
	s := string(b)
	switch s {
	case "\x1b":
		return []Key{{Name: "esc"}}
	case "\x1b[A", "\x1bOA":
		return []Key{{Name: "up"}}
	case "\x1b[B", "\x1bOB":
		return []Key{{Name: "down"}}
	case "\x1b[C", "\x1bOC":
		return []Key{{Name: "right"}}
	}
	if strings.HasPrefix(s, "\x1b") {
		return nil
	}
	var keys []Key
	for _, r := range s {
		switch r {
		case '\r', '\n':
			keys = append(keys, Key{Name: "enter"})
		case 3:
			keys = append(keys, Key{Name: "ctrl-c"})
		case 127, 8:
			keys = append(keys, Key{Name: "backspace"})
		case 14:
			keys = append(keys, Key{Name: "down"})
		case 16:
			keys = append(keys, Key{Name: "up"})
		default:
			if r >= ' ' {
				keys = append(keys, Key{Rune: r})
			}
		}
	}
	return keys
}

// Watch delivers a value each time a hook signals the deck's wait-for
// channel. Blocking in tmux costs no CPU, unlike polling.
func Watch(ctx context.Context, flags []string, channel string) <-chan struct{} {
	ch := make(chan struct{}, 1)
	go func() {
		for ctx.Err() == nil {
			args := append(append([]string{}, flags...), "wait-for", channel)
			if err := exec.CommandContext(ctx, "tmux", args...).Run(); err != nil {
				return
			}
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}()
	return ch
}
