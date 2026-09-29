package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

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
	mu    sync.Mutex
	input []string
}

// write sends a whole frame in one call, so the terminal never shows half of
// one. A failed write only costs a frame; the next redraw repeats everything.
func write(s string) { _, _ = os.Stdout.WriteString(s) }

// SetTitle names the pane through its terminal title, which tmux shows as
// pane_title; unlike select-pane -T, it does not move focus.
func (t *Term) SetTitle(s string) { write("\x1b]2;" + s + "\x07") }

// OpenTerm switches stdin to raw mode and takes over the screen.
func OpenTerm() (*Term, error) {
	fd := int(os.Stdin.Fd())
	st, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	// SGR mouse reporting, so tmux hands the view wheel events.
	write("\x1b[?1049h\x1b[?25l\x1b[?1000h\x1b[?1006h")
	return &Term{fd: fd, state: st}, nil
}

// Close restores the terminal.
func (t *Term) Close() {
	write("\x1b[?1006l\x1b[?1000l\x1b[?25h\x1b[?1049l")
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

// startGrace drops a lone Esc arriving right after the view opens. The
// terminal can deliver the tail of the key that opened it, or a focus
// report, as a bare ESC byte, which would otherwise close the view at once.
const startGrace = 400 * time.Millisecond

// Keys decodes keypresses from stdin until it closes. Raw input is kept in
// Trace so a view can report what made it close.
func (t *Term) Keys() <-chan Key {
	ch := make(chan Key)
	start := time.Now()
	go func() {
		defer close(ch)
		buf := make([]byte, 64)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				t.trace("read error: " + err.Error())
				return
			}
			t.trace(fmt.Sprintf("%q", buf[:n]))
			for _, k := range decode(buf[:n]) {
				if k.Name == "esc" && time.Since(start) < startGrace {
					continue
				}
				ch <- k
			}
		}
	}()
	return ch
}

func (t *Term) trace(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.input = append(t.input, s)
	if len(t.input) > 8 {
		t.input = t.input[1:]
	}
}

// Trace is the most recent raw input, oldest first.
func (t *Term) Trace() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.input...)
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
	if strings.HasPrefix(s, "\x1b[<") {
		return mouse(s)
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

// mouse turns SGR mouse reports ("\x1b[<64;x;yM") into wheel keys. Clicks
// are left to tmux, which focuses the pane.
func mouse(s string) []Key {
	var keys []Key
	for _, ev := range strings.Split(s, "\x1b[<")[1:] {
		button, _, _ := strings.Cut(ev, ";")
		switch button {
		case "64":
			keys = append(keys, Key{Name: "wheelup"})
		case "65":
			keys = append(keys, Key{Name: "wheeldown"})
		}
	}
	return keys
}
