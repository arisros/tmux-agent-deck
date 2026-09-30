package record

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 30, 1, 2, 3, 0, time.UTC)

func TestFromHookRedacts(t *testing.T) {
	in := `{
		"session_id": "3f1c-secret-session",
		"transcript_path": "/Users/someone/.claude/projects/-work-acme/x.jsonl",
		"cwd": "/Users/someone/work/acme/acme-workspace",
		"hook_event_name": "PreToolUse",
		"tool_name": "mcp__atlassian__getJiraIssue",
		"tool_input": {"issueKey": "BL-1234", "command": "cat /etc/passwd"},
		"prompt": "fix the acme billing flow"
	}`
	e, err := FromHook(strings.NewReader(in), "%12", now)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(e)
	for _, leak := range []string{"secret", "/Users", "acme", "BL-1234", "passwd", "billing", "atlassian", "Jira"} {
		if strings.Contains(string(out), leak) {
			t.Errorf("entry leaks %q: %s", leak, out)
		}
	}
	if e.Event != "PreToolUse" || e.Tool != "mcp" || e.Pane != "%12" || len(e.Session) != 12 {
		t.Errorf("unexpected entry: %+v", e)
	}
	want := []string{"cwd", "hook_event_name", "prompt", "session_id", "tool_input", "tool_name", "transcript_path"}
	if strings.Join(e.Keys, ",") != strings.Join(want, ",") {
		t.Errorf("keys = %v, want %v", e.Keys, want)
	}
}

func TestFromHookKeepsEventMetadata(t *testing.T) {
	cases := []struct {
		in   string
		want Entry
	}{
		{`{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs permission to use Bash"}`,
			Entry{Event: "Notification", NotificationType: "permission_prompt"}},
		{`{"hook_event_name":"SessionStart","source":"resume"}`, Entry{Event: "SessionStart", Source: "resume"}},
		{`{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`, Entry{Event: "SessionEnd", Reason: "prompt_input_exit"}},
		{`{"hook_event_name":"SubagentStart","agent_type":"Explore"}`, Entry{Event: "SubagentStart", AgentType: "Explore"}},
		{`{"hook_event_name":"PreToolUse","tool_name":"Bash"}`, Entry{Event: "PreToolUse", Tool: "Bash"}},
	}
	for _, c := range cases {
		e, err := FromHook(strings.NewReader(c.in), "", now)
		if err != nil {
			t.Fatal(err)
		}
		if e.Event != c.want.Event || e.NotificationType != c.want.NotificationType || e.Source != c.want.Source ||
			e.Reason != c.want.Reason || e.AgentType != c.want.AgentType || e.Tool != c.want.Tool {
			t.Errorf("%s: got %+v", c.in, e)
		}
		out, _ := json.Marshal(e)
		if strings.Contains(string(out), "needs permission") {
			t.Errorf("message text leaked: %s", out)
		}
	}
}

func TestFromHookRejectsGarbage(t *testing.T) {
	if _, err := FromHook(strings.NewReader("not json"), "", now); err == nil {
		t.Fatal("want error")
	}
}

func TestAppendWritesOneLinePerEntry(t *testing.T) {
	dir := t.TempDir()
	for _, ev := range []string{"Stop", "SessionEnd"} {
		if err := Append(dir, Entry{TS: now.UnixMilli(), Event: ev, Keys: []string{}}); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "2026-09-30.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines: %q", len(lines), b)
	}
	fi, _ := os.Stat(filepath.Join(dir, "2026-09-30.jsonl"))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestFromHookCountsBackgroundWorkWithoutKeepingIt(t *testing.T) {
	cases := []struct {
		field string
		want  *int
	}{
		{`"background_tasks":[{"id":"b1","command":"go test ./acme/..."},{"id":"b2"}]`, intp(2)},
		{`"background_tasks":{"b1":{"command":"sleep 99"}}`, intp(1)},
		{`"background_tasks":[]`, intp(0)},
		{`"background_tasks":3`, intp(3)},
		{`"background_tasks":null`, nil},
		{`"other":1`, nil},
	}
	for _, c := range cases {
		in := `{"hook_event_name":"Stop",` + c.field + `,"session_crons":[{"cron":"* * * * *"}]}`
		e, err := FromHook(strings.NewReader(in), "", now)
		if err != nil {
			t.Fatal(err)
		}
		if (e.BackgroundTasks == nil) != (c.want == nil) || (c.want != nil && *e.BackgroundTasks != *c.want) {
			t.Errorf("%s: background_tasks = %v, want %v", c.field, deref(e.BackgroundTasks), deref(c.want))
		}
		if e.SessionCrons == nil || *e.SessionCrons != 1 {
			t.Errorf("%s: session_crons = %v, want 1", c.field, deref(e.SessionCrons))
		}
		out, _ := json.Marshal(e)
		for _, leak := range []string{"acme", "sleep", "* * *", "b1"} {
			if strings.Contains(string(out), leak) {
				t.Errorf("entry leaks %q: %s", leak, out)
			}
		}
	}
}

func intp(n int) *int { return &n }

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
