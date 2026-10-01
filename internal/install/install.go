// Package install adds and removes tmux-agent-deck hooks in a Claude Code
// settings file without disturbing anything else in it.
//
// The file is edited as an ordered JSON tree: key order and every value this
// package does not own are carried over untouched, so the diff a user reviews
// only shows the deck's own hook entries.
package install

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Marker identifies hook commands owned by this plugin.
const Marker = "tmux-agent-deck"

// Hook describes the command registered on each event.
type Hook struct {
	Command string
	Async   bool
	Timeout int
}

// Add registers h on every event, replacing any earlier deck entry, and
// returns the new file content.
func Add(settings []byte, events []string, h Hook) ([]byte, error) {
	if !strings.Contains(h.Command, Marker) {
		return nil, fmt.Errorf("command %q does not contain marker %q", h.Command, Marker)
	}
	cleaned, err := Remove(settings)
	if err != nil {
		return nil, err
	}
	root, err := parseObject(cleaned)
	if err != nil {
		return nil, err
	}
	hooks := object{}
	if raw, ok := root.get("hooks"); ok {
		if hooks, err = parseObject(raw); err != nil {
			return nil, fmt.Errorf("hooks: %w", err)
		}
	}

	entry := object{{"type", mustJSON("command")}, {"command", mustJSON(h.Command)}}
	if h.Async {
		entry = append(entry, member{"async", mustJSON(true)})
	}
	if h.Timeout > 0 {
		entry = append(entry, member{"timeout", mustJSON(h.Timeout)})
	}
	group := object{{"hooks", mustJSON([]json.RawMessage{entry.raw()})}}

	for _, ev := range events {
		var groups []json.RawMessage
		if raw, ok := hooks.get(ev); ok {
			if err := json.Unmarshal(raw, &groups); err != nil {
				return nil, fmt.Errorf("hooks.%s: %w", ev, err)
			}
		}
		groups = append(groups, group.raw())
		hooks = hooks.set(ev, mustJSON(groups))
	}
	root = root.set("hooks", hooks.raw())
	return format(root.raw())
}

// Remove deletes every deck hook, dropping groups and events it leaves empty.
func Remove(settings []byte) ([]byte, error) {
	if len(bytes.TrimSpace(settings)) == 0 {
		settings = []byte("{}")
	}
	root, err := parseObject(settings)
	if err != nil {
		return nil, err
	}
	raw, ok := root.get("hooks")
	if !ok {
		return format(root.raw())
	}
	hooks, err := parseObject(raw)
	if err != nil {
		return nil, fmt.Errorf("hooks: %w", err)
	}
	out := object{}
	for _, ev := range hooks {
		var groups []json.RawMessage
		if err := json.Unmarshal(ev.Value, &groups); err != nil {
			return nil, fmt.Errorf("hooks.%s: %w", ev.Key, err)
		}
		kept := make([]json.RawMessage, 0, len(groups))
		for _, g := range groups {
			ng, keep, err := withoutDeck(g)
			if err != nil {
				return nil, fmt.Errorf("hooks.%s: %w", ev.Key, err)
			}
			if keep {
				kept = append(kept, ng)
			}
		}
		if len(kept) > 0 {
			out = append(out, member{ev.Key, mustJSON(kept)})
		}
	}
	if len(out) == 0 {
		// Nothing left but what the deck added: leave no empty "hooks" behind.
		kept := object{}
		for _, m := range root {
			if m.Key != "hooks" {
				kept = append(kept, m)
			}
		}
		return format(kept.raw())
	}
	root = root.set("hooks", out.raw())
	return format(root.raw())
}

// Owned reports which events currently carry a deck hook.
func Owned(settings []byte) ([]string, error) {
	root, err := parseObject(settings)
	if err != nil {
		return nil, err
	}
	raw, ok := root.get("hooks")
	if !ok {
		return nil, nil
	}
	hooks, err := parseObject(raw)
	if err != nil {
		return nil, err
	}
	var events []string
	for _, ev := range hooks {
		if bytes.Contains(ev.Value, []byte(Marker)) {
			events = append(events, ev.Key)
		}
	}
	return events, nil
}

func withoutDeck(groupRaw json.RawMessage) (json.RawMessage, bool, error) {
	group, err := parseObject(groupRaw)
	if err != nil {
		return nil, false, err
	}
	hraw, ok := group.get("hooks")
	if !ok {
		return groupRaw, true, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(hraw, &entries); err != nil {
		return nil, false, err
	}
	kept := entries[:0]
	for _, e := range entries {
		var c struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(e, &c); err != nil {
			return nil, false, err
		}
		if !strings.Contains(c.Command, Marker) {
			kept = append(kept, e)
		}
	}
	if len(kept) == len(entries) {
		return groupRaw, true, nil
	}
	if len(kept) == 0 {
		return nil, false, nil
	}
	return group.set("hooks", mustJSON(kept)).raw(), true, nil
}

type member struct {
	Key   string
	Value json.RawMessage
}

// object is a JSON object that remembers its key order.
type object []member

func parseObject(b []byte) (object, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("expected a JSON object")
	}
	var o object
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o = append(o, member{tok.(string), v})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o object) get(k string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.Key == k {
			return m.Value, true
		}
	}
	return nil, false
}

func (o object) set(k string, v json.RawMessage) object {
	for i := range o {
		if o[i].Key == k {
			o[i].Value = v
			return o
		}
	}
	return append(o, member{k, v})
}

func (o object) raw() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(mustJSON(m.Key))
		b.WriteByte(':')
		b.Write(m.Value)
	}
	b.WriteByte('}')
	return b.Bytes()
}

// mustJSON encodes without HTML escaping: json.Marshal would rewrite the "&&"
// in a user's existing hook commands to escaped \u0026 sequences, even inside a
// RawMessage, and turn the reviewed diff into noise.
func mustJSON(v any) json.RawMessage {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimRight(b.Bytes(), "\n")
}

// format matches the two-space layout Claude Code and jq write.
func format(b []byte) ([]byte, error) {
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// SetStatusLine points Claude's statusLine at command, unless the user has a
// status line of their own: that one is theirs, and the deck only reads
// usage when it owns the slot. It reports whether the deck owns it after.
func SetStatusLine(settings []byte, command string) ([]byte, bool, error) {
	if len(bytes.TrimSpace(settings)) == 0 {
		settings = []byte("{}")
	}
	root, err := parseObject(settings)
	if err != nil {
		return nil, false, err
	}
	if raw, ok := root.get("statusLine"); ok && !bytes.Contains(raw, []byte(Marker)) {
		out, err := format(root.raw())
		return out, false, err
	}
	sl := object{{"type", mustJSON("command")}, {"command", mustJSON(command)}, {"padding", mustJSON(0)}}
	root = root.set("statusLine", sl.raw())
	out, err := format(root.raw())
	return out, true, err
}

// WrapFlag marks a status line command that wraps the user's own. The
// original command follows it, base64 encoded, so uninstall can put it back
// byte for byte without parsing shell quoting.
const WrapFlag = "--wrap64"

var wrapped = regexp.MustCompile(WrapFlag + ` ([A-Za-z0-9+/=]+)`)

// WrapStatusLine replaces the user's own statusLine command with wrap(its
// base64), leaving every other field of the statusLine alone. It reports
// false, and changes nothing, when there is no foreign command to wrap.
func WrapStatusLine(settings []byte, wrap func(original64 string) string) ([]byte, bool, error) {
	root, err := parseObject(settings)
	if err != nil {
		return nil, false, err
	}
	raw, ok := root.get("statusLine")
	if !ok || bytes.Contains(raw, []byte(Marker)) {
		return settings, false, nil
	}
	sl, err := parseObject(raw)
	if err != nil {
		return nil, false, fmt.Errorf("statusLine: %w", err)
	}
	var original string
	if c, ok := sl.get("command"); !ok || json.Unmarshal(c, &original) != nil || original == "" {
		return settings, false, nil
	}
	command := wrap(base64.StdEncoding.EncodeToString([]byte(original)))
	if !strings.Contains(command, Marker) || !strings.Contains(command, WrapFlag) {
		return nil, false, fmt.Errorf("wrapping command %q lacks the marker or %s", command, WrapFlag)
	}
	root = root.set("statusLine", sl.set("command", mustJSON(command)).raw())
	out, err := format(root.raw())
	return out, true, err
}

// Unwrap returns the command a wrapping status line command carries.
func Unwrap(command string) (string, bool) {
	m := wrapped.FindStringSubmatch(command)
	if m == nil {
		return "", false
	}
	b, err := base64.StdEncoding.DecodeString(m[1])
	return string(b), err == nil
}

// RemoveStatusLine drops the statusLine if the deck owns it, and puts the
// user's own command back if the deck wraps it.
func RemoveStatusLine(settings []byte) ([]byte, error) {
	if len(bytes.TrimSpace(settings)) == 0 {
		settings = []byte("{}")
	}
	root, err := parseObject(settings)
	if err != nil {
		return nil, err
	}
	if raw, ok := root.get("statusLine"); ok && bytes.Contains(raw, []byte(WrapFlag)) {
		if sl, err := parseObject(raw); err == nil {
			var command string
			if c, ok := sl.get("command"); ok && json.Unmarshal(c, &command) == nil {
				if original, ok := Unwrap(command); ok {
					root = root.set("statusLine", sl.set("command", mustJSON(original)).raw())
					return format(root.raw())
				}
			}
		}
	}
	if raw, ok := root.get("statusLine"); ok && bytes.Contains(raw, []byte(Marker)) {
		kept := object{}
		for _, m := range root {
			if m.Key != "statusLine" {
				kept = append(kept, m)
			}
		}
		root = kept
	}
	return format(root.raw())
}
