package usage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Shaped like the documented statusLine input.
const input = `{
  "session_id": "abc-123",
  "transcript_path": "/Users/someone/.claude/projects/x/abc-123.jsonl",
  "cwd": "/Users/someone/work",
  "model": {"id": "claude-opus-5-5", "display_name": "Opus 5.5"},
  "cost": {"total_cost_usd": 1.2345, "total_duration_ms": 1000},
  "context_window": {"total_input_tokens": 120000, "total_output_tokens": 8500, "used_percentage": 42.4},
  "rate_limits": {
    "five_hour": {"used_percentage": 31, "resets_at": 1790712000},
    "seven_day": {"used_percentage": 12.5, "resets_at": "2026-10-03T08:00:00Z"}
  }
}`

var now = time.Unix(1790700000, 0)

func TestRecordAndLoad(t *testing.T) {
	dir := t.TempDir()
	in, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if err := Record(dir, in, now); err != nil {
		t.Fatal(err)
	}
	sessions, limits := Load(dir)
	s, ok := sessions["abc-123"]
	if !ok || s.Model != "Opus 5.5" || s.CostUSD != 1.2345 || s.InputTokens != 120000 || *s.ContextUsed != 42.4 {
		t.Fatalf("session = %+v", s)
	}
	if limits == nil || limits.FiveHour.UsedPercentage != 31 || limits.FiveHour.ResetsAt.Unix() != 1790712000 {
		t.Fatalf("limits = %+v", limits)
	}
	if got := limits.SevenDay.ResetsAt.UTC().Format(time.RFC3339); got != "2026-10-03T08:00:00Z" {
		t.Errorf("seven-day reset = %s", got)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "abc-123.json"))
	for _, leak := range []string{"transcript", "/Users", "someone"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("usage file keeps %q: %s", leak, b)
		}
	}
}

func TestResetTimeInMilliseconds(t *testing.T) {
	in, err := Parse(strings.NewReader(`{"rate_limits":{"five_hour":{"used_percentage":1,"resets_at":1790712000000}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := in.RateLimits.FiveHour.ResetsAt.Unix(); got != 1790712000 {
		t.Errorf("reset = %d", got)
	}
}

func TestInputWithoutLimitsKeepsTheLastKnown(t *testing.T) {
	dir := t.TempDir()
	full, _ := Parse(strings.NewReader(input))
	_ = Record(dir, full, now)
	bare, _ := Parse(strings.NewReader(`{"session_id":"other","model":{"display_name":"Sonnet 5.5"}}`))
	_ = Record(dir, bare, now.Add(time.Minute))
	sessions, limits := Load(dir)
	if len(sessions) != 2 || limits == nil || limits.FiveHour.UsedPercentage != 31 {
		t.Errorf("sessions %d, limits %+v", len(sessions), limits)
	}
}

func TestRejectsUnsafeSessionID(t *testing.T) {
	dir := t.TempDir()
	in, _ := Parse(strings.NewReader(`{"session_id":"../../evil","model":{"display_name":"x"}}`))
	_ = Record(dir, in, now)
	if files, _ := filepath.Glob(filepath.Join(dir, "*")); len(files) != 0 {
		t.Errorf("wrote %v", files)
	}
}

func TestLine(t *testing.T) {
	in, _ := Parse(strings.NewReader(input))
	if got := Line(in); got != "Opus 5.5 · ctx 42% · 5h 31% · 7d 12%" && got != "Opus 5.5 · ctx 42% · 5h 31% · 7d 13%" {
		t.Errorf("line = %q", got)
	}
}

func TestTokens(t *testing.T) {
	for n, want := range map[int64]string{950: "950", 12_345: "12.3k", 1_250_000: "1.2M"} {
		if got := Tokens(n); got != want {
			t.Errorf("Tokens(%d) = %s, want %s", n, got, want)
		}
	}
}
