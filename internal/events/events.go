// Package events is the deck's log of state changes: one JSON line per change,
// appended by hooks, focus changes and screen checks, and read by `deck
// events`. It holds states, causes and tool names, never prompts or arguments.
package events

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Event is one state change of one agent.
type Event struct {
	// TS is unix milliseconds.
	TS   int64  `json:"ts"`
	SID  string `json:"sid,omitempty"`
	Pane string `json:"pane,omitempty"`
	// Kind is the machine event that caused the change, or Begin, End or
	// Discover.
	Kind   string `json:"event"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Source string `json:"src,omitempty"`
	Reason string `json:"reason,omitempty"`
	Tool   string `json:"tool,omitempty"`
}

// Kinds that are not machine events.
const (
	Begin    = "Begin"
	End      = "End"
	Discover = "Discover"
)

const (
	name    = "events.jsonl"
	maxSize = 256 << 10
)

// Path is the log file under root.
func Path(root string) string { return filepath.Join(root, name) }

// Append writes e as one JSON line. A single O_APPEND write keeps concurrent
// hooks from interleaving lines.
func Append(root string, e Event) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(Path(root), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

// Rotate keeps one previous log once the current one passes 256 KB.
func Rotate(root string) {
	p := Path(root)
	if fi, err := os.Stat(p); err == nil && fi.Size() > maxSize {
		_ = os.Rename(p, p+".1")
	}
}

// Writer returns the function a Deck emits through. It rotates only when a
// session begins, so no other hook pays for the stat.
func Writer(root string) func(Event) {
	return func(e Event) {
		if e.Kind == Begin {
			Rotate(root)
		}
		_ = Append(root, e)
	}
}

// Read returns the previous log followed by the current one, oldest first.
// Lines that do not parse are skipped.
func Read(root string) ([]Event, error) {
	var out []Event
	for _, p := range []string{Path(root) + ".1", Path(root)} {
		f, err := os.Open(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		r := bufio.NewReader(f)
		for {
			line, err := r.ReadBytes('\n')
			var e Event
			if json.Unmarshal(line, &e) == nil && e.Kind != "" {
				out = append(out, e)
			}
			if err != nil {
				break
			}
		}
		f.Close()
	}
	return out, nil
}
