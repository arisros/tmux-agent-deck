// Package store keeps one record per Claude session: the pane it runs in and
// its persisted machine snapshot.
//
// Hooks for one session can run concurrently (parallel tool calls fire
// PreToolUse together), so every read-modify-write holds an exclusive flock on
// the session's file.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

// Record is what the deck remembers about a session.
type Record struct {
	Pane     string          `json:"pane"`
	Snapshot json.RawMessage `json:"snapshot,omitempty"`
}

// DefaultDir is under the user's state directory rather than $TMPDIR: Claude's
// hooks and tmux's run-shell can see different temp directories, and both
// must find the same records.
func DefaultDir() string {
	if d := os.Getenv("DECK_STATE_DIR"); d != "" {
		return d
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "tmux-agent-deck", "sessions")
}

// Prune deletes records untouched for longer than maxAge: sessions whose
// Claude process died without a SessionEnd.
func Prune(dir string, maxAge time.Duration) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	cutoff := time.Now().Add(-maxAge)
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil && fi.ModTime().Before(cutoff) {
			_ = os.Remove(f)
		}
	}
}

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Locked is an open, exclusively locked session record.
type Locked struct {
	f      *os.File
	rec    Record
	exists bool
}

// Open locks the record for sessionID, creating the directory and file when
// needed. Close releases the lock.
func Open(dir, sessionID string) (*Locked, error) {
	if !validID.MatchString(sessionID) {
		return nil, fmt.Errorf("invalid session id %q", sessionID)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, sessionID+".json"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	l := &Locked{f: f}
	b, err := io.ReadAll(f)
	if err != nil {
		l.Close()
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &l.rec); err != nil {
			// A corrupt record only loses one session's state; start over.
			l.rec = Record{}
		} else {
			l.exists = true
		}
	}
	return l, nil
}

// Record returns the stored record and whether one existed.
func (l *Locked) Record() (Record, bool) { return l.rec, l.exists }

// Save replaces the record in place, so the lock stays on the same inode.
func (l *Locked) Save(r Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := l.f.Truncate(0); err != nil {
		return err
	}
	if _, err := l.f.WriteAt(b, 0); err != nil {
		return err
	}
	l.rec, l.exists = r, true
	return nil
}

// Delete removes the record. The lock is released on Close.
func (l *Locked) Delete() error {
	err := os.Remove(l.f.Name())
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	l.exists = false
	return err
}

// Close releases the lock.
func (l *Locked) Close() error { return l.f.Close() }
