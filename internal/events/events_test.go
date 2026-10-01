package events

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentAppendsStayWholeLines(t *testing.T) {
	root := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := Append(root, Event{TS: int64(i), Kind: "Prompt", Pane: "%1", From: "idle", To: "running"}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	f, err := os.Open(Path(root))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	for sc := bufio.NewScanner(f); sc.Scan(); n++ {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line %d is not one event: %q", n, sc.Text())
		}
	}
	if n != 50 {
		t.Errorf("%d lines, want 50", n)
	}
}

func TestWriterRotatesOnlyWhenASessionBegins(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(Path(root), []byte(strings.Repeat("x", maxSize+1)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := Writer(root)
	w(Event{TS: 1, Kind: "Stop", To: "done"})
	if _, err := os.Stat(Path(root) + ".1"); err == nil {
		t.Fatal("a Stop rotated the log: the hot path must not pay for it")
	}
	w(Event{TS: 2, Kind: Begin, To: "idle"})
	if _, err := os.Stat(Path(root) + ".1"); err != nil {
		t.Fatalf("Begin did not rotate a full log: %v", err)
	}
	got, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Kind != "Stop" || got[1].Kind != Begin {
		t.Errorf("Read = %+v, want the Stop from the old log, then the Begin", got)
	}
}

func TestReadFromContinuesAndSurvivesRotation(t *testing.T) {
	root := t.TempDir()
	if got, off, err := ReadFrom(root, 0); err != nil || len(got) != 0 || off != 0 {
		t.Fatalf("no log yet: %v %d %v", got, off, err)
	}
	_ = Append(root, Event{TS: 1, Kind: "Prompt", To: "running"})
	off := Size(root)
	_ = Append(root, Event{TS: 2, Kind: "Stop", To: "done"})
	// A line still being written is not an event yet.
	f, _ := os.OpenFile(Path(root), os.O_WRONLY|os.O_APPEND, 0o600)
	_, _ = f.WriteString(`{"ts":3,"event":"Half`)
	got, next, err := ReadFrom(root, off)
	if err != nil || len(got) != 1 || got[0].Kind != "Stop" {
		t.Fatalf("ReadFrom = %+v %v", got, err)
	}
	_, _ = f.WriteString("way\"}\n")
	f.Close()
	if got, _, _ := ReadFrom(root, next); len(got) != 1 || got[0].Kind != "Halfway" {
		t.Errorf("the finished line was lost: %+v", got)
	}
	// Rotation replaces the file with a shorter one.
	_ = os.Rename(Path(root), Path(root)+".1")
	_ = Append(root, Event{TS: 4, Kind: Begin, To: "idle"})
	if got, _, _ := ReadFrom(root, next+100); len(got) != 1 || got[0].Kind != Begin {
		t.Errorf("after rotation: %+v", got)
	}
}

func TestReadWithoutALog(t *testing.T) {
	got, err := Read(t.TempDir())
	if err != nil || len(got) != 0 {
		t.Errorf("Read = %v, %v", got, err)
	}
}
