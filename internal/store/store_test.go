package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func TestSaveAndReopen(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l.Record(); ok {
		t.Fatal("new record reported as existing")
	}
	if err := l.Save(Record{Pane: "%1", Snapshot: json.RawMessage(`{"v":1}`)}); err != nil {
		t.Fatal(err)
	}
	l.Close()

	l, _ = Open(dir, "s1")
	r, ok := l.Record()
	if !ok || r.Pane != "%1" || string(r.Snapshot) != `{"v":1}` {
		t.Fatalf("got %+v %v", r, ok)
	}
	// A shorter record must not leave the old tail behind.
	if err := l.Save(Record{Pane: "%2"}); err != nil {
		t.Fatal(err)
	}
	l.Close()
	l, _ = Open(dir, "s1")
	if r, _ := l.Record(); r.Pane != "%2" || r.Snapshot != nil {
		t.Fatalf("got %+v", r)
	}
	if err := l.Delete(); err != nil {
		t.Fatal(err)
	}
	l.Close()
}

// Parallel tool calls fire concurrent hooks for one session; the lock must
// serialize their read-modify-write so no update is lost.
func TestConcurrentUpdatesAreSerialized(t *testing.T) {
	dir := t.TempDir()
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := Open(dir, "s1")
			if err != nil {
				t.Error(err)
				return
			}
			defer l.Close()
			r, _ := l.Record()
			c, _ := strconv.Atoi(r.Pane)
			if err := l.Save(Record{Pane: strconv.Itoa(c + 1)}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	l, _ := Open(dir, "s1")
	defer l.Close()
	if r, _ := l.Record(); r.Pane != strconv.Itoa(n) {
		t.Fatalf("counter = %s, want %d", r.Pane, n)
	}
}

func TestRejectsUnsafeSessionID(t *testing.T) {
	for _, id := range []string{"", "../x", "a/b", "a b"} {
		if _, err := Open(t.TempDir(), id); err == nil {
			t.Errorf("accepted %q", id)
		}
	}
}

func TestCorruptRecordStartsOver(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir, "s1")
	_ = l.Save(Record{Pane: "%1"})
	_, _ = l.f.WriteAt([]byte("{broken"), 0)
	l.Close()
	l, err := Open(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, ok := l.Record(); ok {
		t.Error("corrupt record reported as existing")
	}
}

func TestOpenExistingNeverCreates(t *testing.T) {
	dir := t.TempDir()
	if _, err := OpenExisting(dir, "gone"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want not exist", err)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*")); len(files) != 0 {
		t.Errorf("created %v", files)
	}
	l, _ := Open(dir, "here")
	_ = l.Save(Record{Pane: "%1"})
	l.Close()
	l, err := OpenExisting(dir, "here")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if r, ok := l.Record(); !ok || r.Pane != "%1" {
		t.Errorf("got %+v %v", r, ok)
	}
}
