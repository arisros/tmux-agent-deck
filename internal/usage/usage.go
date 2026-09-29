// Package usage keeps what Claude Code reports through its statusLine
// command: per-session context and cost, and the plan's rate limits.
//
// The statusLine input is documented, unlike the transcript files, whose
// format Claude Code calls internal. Claude runs the command on every status
// update, so the numbers stay fresh without the deck polling anything.
package usage

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Input is the part of Claude Code's statusLine JSON the deck reads.
type Input struct {
	SessionID string `json:"session_id"`
	Model     struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Cost struct {
		TotalCostUSD float64 `json:"total_cost_usd"`
	} `json:"cost"`
	ContextWindow struct {
		TotalInputTokens  int64    `json:"total_input_tokens"`
		TotalOutputTokens int64    `json:"total_output_tokens"`
		UsedPercentage    *float64 `json:"used_percentage"`
	} `json:"context_window"`
	RateLimits struct {
		FiveHour *Window `json:"five_hour"`
		SevenDay *Window `json:"seven_day"`
	} `json:"rate_limits"`
}

// Window is one rate-limit window of the plan.
type Window struct {
	UsedPercentage float64   `json:"used_percentage"`
	ResetsAt       ResetTime `json:"resets_at"`
}

// ResetTime accepts unix seconds, unix milliseconds, or an RFC 3339 string:
// the field's encoding is not spelled out, so the deck reads all of them.
type ResetTime struct{ time.Time }

// UnmarshalJSON implements json.Unmarshaler.
func (r *ResetTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		if n > 1e12 {
			n /= 1000
		}
		r.Time = time.Unix(int64(n), 0)
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return fmt.Errorf("resets_at: %w", err)
	}
	r.Time = t
	return nil
}

// MarshalJSON implements json.Marshaler.
func (r ResetTime) MarshalJSON() ([]byte, error) {
	if r.IsZero() {
		return []byte("null"), nil
	}
	return []byte(strconv.FormatInt(r.Unix(), 10)), nil
}

// Session is what the deck keeps per Claude session.
type Session struct {
	Model         string   `json:"model"`
	CostUSD       float64  `json:"cost_usd"`
	InputTokens   int64    `json:"input_tokens"`
	OutputTokens  int64    `json:"output_tokens"`
	ContextUsed   *float64 `json:"context_used,omitempty"`
	UpdatedAtUnix int64    `json:"updated_at"`
}

// Limits is the plan's usage, shared by every session of the account.
type Limits struct {
	FiveHour      *Window `json:"five_hour,omitempty"`
	SevenDay      *Window `json:"seven_day,omitempty"`
	UpdatedAtUnix int64   `json:"updated_at"`
}

// Parse reads one statusLine input.
func Parse(r io.Reader) (Input, error) {
	var in Input
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return Input{}, fmt.Errorf("decode statusLine input: %w", err)
	}
	return in, nil
}

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Record saves the session's usage and, when present, the plan's limits.
func Record(dir string, in Input, now time.Time) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if validID.MatchString(in.SessionID) {
		s := Session{
			Model:         in.Model.DisplayName,
			CostUSD:       in.Cost.TotalCostUSD,
			InputTokens:   in.ContextWindow.TotalInputTokens,
			OutputTokens:  in.ContextWindow.TotalOutputTokens,
			ContextUsed:   in.ContextWindow.UsedPercentage,
			UpdatedAtUnix: now.Unix(),
		}
		if s.Model == "" {
			s.Model = in.Model.ID
		}
		if err := writeJSON(filepath.Join(dir, in.SessionID+".json"), s); err != nil {
			return err
		}
	}
	if in.RateLimits.FiveHour != nil || in.RateLimits.SevenDay != nil {
		l := Limits{FiveHour: in.RateLimits.FiveHour, SevenDay: in.RateLimits.SevenDay, UpdatedAtUnix: now.Unix()}
		return writeJSON(filepath.Join(dir, "limits.json"), l)
	}
	return nil
}

// Load reads every session's usage and the latest limits.
func Load(dir string) (map[string]Session, *Limits) {
	sessions := map[string]Session{}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var limits *Limits
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		if name == "limits" {
			var l Limits
			if json.Unmarshal(b, &l) == nil {
				limits = &l
			}
			continue
		}
		var s Session
		if json.Unmarshal(b, &s) == nil {
			sessions[name] = s
		}
	}
	return sessions, limits
}

// Prune deletes session usage untouched for longer than maxAge.
func Prune(dir string, maxAge time.Duration) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	cutoff := time.Now().Add(-maxAge)
	for _, f := range files {
		if filepath.Base(f) == "limits.json" {
			continue
		}
		if fi, err := os.Stat(f); err == nil && fi.ModTime().Before(cutoff) {
			_ = os.Remove(f)
		}
	}
}

// DefaultDir sits next to the session records.
func DefaultDir(stateDir string) string { return filepath.Join(filepath.Dir(stateDir), "usage") }

// writeJSON replaces a file atomically: views read these while Claude writes.
func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".usage-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Line is the status line Claude shows under its prompt.
func Line(in Input) string {
	parts := []string{}
	if m := in.Model.DisplayName; m != "" {
		parts = append(parts, m)
	}
	if p := in.ContextWindow.UsedPercentage; p != nil {
		parts = append(parts, fmt.Sprintf("ctx %.0f%%", *p))
	}
	if w := in.RateLimits.FiveHour; w != nil {
		parts = append(parts, fmt.Sprintf("5h %.0f%%", w.UsedPercentage))
	}
	if w := in.RateLimits.SevenDay; w != nil {
		parts = append(parts, fmt.Sprintf("7d %.0f%%", w.UsedPercentage))
	}
	return strings.Join(parts, " · ")
}

// Tokens formats a token count compactly: 950, 12.3k, 1.2M.
func Tokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return strconv.FormatInt(n, 10)
}
