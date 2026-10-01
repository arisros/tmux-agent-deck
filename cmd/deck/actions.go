package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/arisros/tmux-agent-deck/internal/deck"
	"github.com/arisros/tmux-agent-deck/internal/tmux"
	"github.com/arisros/tmux-agent-deck/internal/ui"
)

// Everything the deck types into a pane goes through ifAlive: tmux checks,
// in the same call that sends, that the pane still runs the agent the deck
// heard from. A pane whose agent exited is a shell, and text sent there
// would run as a command.
func ifAlive(c tmux.Client, pane, then, otherwise string) error {
	_, err := c.Run("if-shell", "-F", "-t", pane, tmux.AliveFormat, then, otherwise)
	return err
}

// sendKey presses one named key in an agent's pane. key is always one of the
// deck's own constants, never user text: it becomes part of a tmux command.
func sendKey(c tmux.Client, pane, key string) error {
	return ifAlive(c, pane, "send-keys -t "+pane+" "+key, "")
}

// sendText delivers text to an agent as a prompt and submits it. The text
// travels through a tmux buffer loaded from stdin, so nothing in it is ever
// parsed as a tmux command or a shell word.
func sendText(c tmux.Client, pane, text string, submit bool) error {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return errors.New("nothing to send")
	}
	buf := fmt.Sprintf("deck-send-%d", os.Getpid())
	if _, err := c.RunInput(text, "load-buffer", "-b", buf, "-"); err != nil {
		return err
	}
	if err := ifAlive(c, pane, "paste-buffer -d -p -b "+buf+" -t "+pane, "delete-buffer -b "+buf); err != nil {
		return err
	}
	if !submit {
		return nil
	}
	// The agent reads the paste before the key that submits it.
	time.Sleep(120 * time.Millisecond)
	return sendKey(c, pane, "Enter")
}

// act performs what a key in a view asked for on the agent under the cursor.
// It reports whether the agent's screen is about to change.
func act(d *deck.Deck, c tmux.Client, l *ui.List, o ui.Outcome) bool {
	r, ok := l.Selected()
	if !ok {
		return false
	}
	switch o {
	case ui.Seen:
		_ = d.Focus(r.ID)
	case ui.Interrupt:
		_ = sendKey(c, r.ID, "Escape")
	case ui.Send:
		_ = sendText(c, r.ID, l.Reply, true)
	case ui.Answer:
		if len(l.Reply) == 1 && l.Reply[0] >= '1' && l.Reply[0] <= '9' {
			_ = sendKey(c, r.ID, l.Reply)
		}
	default:
		return false
	}
	l.Reply = ""
	return true
}

func runSend(args []string, stdin io.Reader) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	noEnter := fs.Bool("no-enter", false, "leave the text in the agent's input box")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return errors.New("usage: deck send [--no-enter] <pane> [text...]  (text from stdin when omitted)")
	}
	pane, text := fs.Arg(0), strings.Join(fs.Args()[1:], " ")
	if fs.NArg() == 1 {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		text = string(b)
	}
	c := tmux.FromEnv()
	if err := mustBeAgent(c, pane); err != nil {
		return err
	}
	return sendText(c, pane, text, !*noEnter)
}

func runInterrupt(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: deck interrupt <pane>")
	}
	c := tmux.FromEnv()
	if err := mustBeAgent(c, args[0]); err != nil {
		return err
	}
	return sendKey(c, args[0], "Escape")
}

// mustBeAgent gives a command line a reason when the pane is not a live
// agent; the guard inside tmux would otherwise just do nothing.
func mustBeAgent(c tmux.Client, pane string) error {
	if !strings.HasPrefix(pane, "%") {
		return fmt.Errorf("%q is not a pane id such as %%12 (see deck list)", pane)
	}
	out, err := c.Run("display-message", "-p", "-t", pane, "#{@deck_state} "+tmux.AliveFormat)
	if err != nil {
		return err
	}
	f := strings.Fields(out)
	if len(f) != 2 || f[1] != "1" {
		return fmt.Errorf("pane %s runs no agent the deck knows", pane)
	}
	return nil
}
