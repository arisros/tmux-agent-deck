// Package fixtures holds recorded hook sequences and guards them: the repo is
// public, and recordings come from real work sessions.
package fixtures

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var forbidden = append([]*regexp.Regexp{
	regexp.MustCompile(`/Users/|/home/|/root/|/tmp/|/private/|/var/folders/|[A-Za-z]:\\`),
	regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.]+`),
	regexp.MustCompile(`"(prompt|tool_input|cwd|transcript_path|message|title)"\s*:`),
	regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b`),                                       // ticket keys such as BL-1234
	regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`), // raw session ids
	regexp.MustCompile(`(?i)\b[\w-]+\.(local|lan|internal|corp)\b`),                    // hostnames
}, privateWords()...)

// privateWords are extra forbidden words that must not appear in this public
// repo even as test data, such as the names of internal projects. They come
// from DECK_LEAK_WORDS (comma separated), set locally and as a CI secret.
func privateWords() []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, w := range strings.Split(os.Getenv("DECK_LEAK_WORDS"), ",") {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, regexp.MustCompile(`(?i)`+regexp.QuoteMeta(w)))
		}
	}
	return out
}

func TestScannerCatchesKnownLeaks(t *testing.T) {
	leaks := []string{
		`{"cwd":"/Users/someone/x"}`,
		`{"path":"/tmp/x"}`,
		`{"host":"MacBook-Pro.local"}`,
		`{"session":"3f1c2a9e-0b7d-4c3e-9a51-6d2f8e4b1c07"}`,
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
