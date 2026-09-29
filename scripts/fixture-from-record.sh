#!/usr/bin/env bash
# Turn a slice of a `deck hook --record` trace into a replayable fixture.
#
#   scripts/fixture-from-record.sh <trace.jsonl> <pane> <from HH:MM:SS> <to HH:MM:SS> > test/fixtures/<name>.jsonl
#
# Keeps only what the fixtures use: time since the first event, the event,
# and its metadata. The recorder already drops prompts, paths and titles;
# test/fixtures/scan_test.go checks the result before it can be committed.
set -euo pipefail
trace=$1 pane=$2 from=$3 to=$4
day=$(basename "$trace" .jsonl)
ms() { date -j -f "%Y-%m-%d %H:%M:%S" "$day $1" +%s 2>/dev/null || date -d "$day $1" +%s; }
lo=$(( $(ms "$from") * 1000 )) hi=$(( $(ms "$to") * 1000 ))
jq -c --arg p "$pane" --argjson lo "$lo" --argjson hi "$hi" '
  select(.pane == $p and .ts >= $lo and .ts <= $hi)' "$trace" |
  jq -sc 'if length == 0 then empty else (.[0].ts) as $t0 | .[] | {
      dt: (.ts - $t0), event,
      notification_type, tool, agent_type, source, reason, background_tasks,
      subagent: ((.keys // []) | index("agent_id") != null)
    } | with_entries(select(.value != null and .value != false)) end'
