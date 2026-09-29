// Package fixtures holds recorded hook sequences and guards them: the repo is
// public, and recordings come from real work sessions.
package fixtures

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var forbidden = []*regexp.Regexp{
	regexp.MustCompile(`/Users/|/home/|[A-Za-z]:\\`),
	regexp.MustCompile(`(?i)lora|bfi|bravo`),
	regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.]+`),
	regexp.MustCompile(`"(prompt|tool_input|cwd|transcript_path|message|title)"\s*:`),
	regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b`), // ticket keys such as BL-1234
}

func TestScannerCatchesKnownLeaks(t *testing.T) {
	leaks := []string{
		`{"cwd":"/Users/someone/x"}`,
		`{"note":"lora-workspace"}`,
		`{"who":"someone@example.com"}`,
		`{"prompt": "hi"}`,
		`{"ticket":"BL-1234"}`,
	}
	clean := `{"ts":1,"event":"PreToolUse","session":"a1b2c3d4e5f6","pane":"%12","tool":"Bash","keys":["cwd","prompt","tool_input"]}`
	for _, l := range leaks {
		if !matchesAny(l) {
			t.Errorf("scanner misses %s", l)
		}
	}
	if matchesAny(clean) {
		t.Errorf("scanner flags a clean entry: %s", clean)
	}
}

func matchesAny(s string) bool {
	for _, re := range forbidden {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func TestFixturesCarryNoWorkData(t *testing.T) {
	files, err := filepath.Glob("*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range forbidden {
			if m := re.Find(b); m != nil {
				t.Errorf("%s contains %q (matched %s)", f, m, re)
			}
		}
	}
}
