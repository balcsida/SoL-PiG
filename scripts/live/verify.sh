#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 SoL-PiG contributors
# SPDX-License-Identifier: MIT
#
# Zero-spend live check of SoL-PiG inside a real PiG process.
#
# Usage: scripts/live/verify.sh [work-directory]
#
# Builds and registers the extension through `pig install --validate-only`,
# runs one `pig -p` session against PiG's built-in fake provider with
# ObservationPack enabled, measures the idle footprint of one instance, and
# plays a scripted scenario through a local OpenAI-compatible endpoint
# (scripts/live/mockprovider) that proves the placeholder and obs_recall.
# Everything runs in a throwaway HOME; no credential or network provider is used.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
WORK="${1:-$ROOT/scripts/live/work/$(date +%Y%m%d-%H%M%S)}"
command -v pig >/dev/null || { echo "pig is not installed" >&2; exit 1; }
command -v go >/dev/null || { echo "go is not installed (PiG builds the extension with it)" >&2; exit 1; }
echo "PiG $(pig --version) · work directory $WORK"

# Go's caches must stay where they are: the fake HOME below would make every run a cold build.
REAL_GOPATH=$(go env GOPATH); REAL_GOCACHE=$(go env GOCACHE); REAL_GOMODCACHE=$(go env GOMODCACHE)

mkdir -p "$WORK"/home/{cwd,pig/agent,cache,config,data} "$WORK/tmp"
export HOME="$WORK/home" USERPROFILE="$WORK/home" PIG_HOME="$WORK/home/pig" PIG_CODING_AGENT_DIR="$WORK/home/pig/agent"
export XDG_CACHE_HOME="$WORK/home/cache" XDG_CONFIG_HOME="$WORK/home/config" XDG_DATA_HOME="$WORK/home/data" TMPDIR="$WORK/tmp"
export PIG_OFFLINE=1 PI_OFFLINE=1 PI_TELEMETRY=0 NO_COLOR=1 TERM=dumb LC_ALL=C
export PIG_TEST_FAUX=1 PIG_TEST_FAUX_SCENARIO=parity-basic
export GOPATH="$REAL_GOPATH" GOCACHE="$REAL_GOCACHE" GOMODCACHE="$REAL_GOMODCACHE"
unset PIG_USE_PI_DIRS PI_CODING_AGENT_DIR

printf '{"packages":["%s"]}\n' "$ROOT" > "$PIG_CODING_AGENT_DIR/settings.json"
printf '{"version":1,"observationPack":true}\n' > "$PIG_CODING_AGENT_DIR/sol-pi.json"

fail() { echo "verify.sh: FAIL: $*" >&2; exit 1; }

# 1. The package validates and registers as sol-pig.
VALIDATE=$(pig install "$ROOT" --validate-only --json 2>&1) || fail "validate-only exited $?: $VALIDATE"
grep -q '"valid": true' <<<"$VALIDATE" || fail "validate-only: $VALIDATE"
grep -q '"name": "sol-pig"' <<<"$VALIDATE" || fail "validate-only name: $VALIDATE"
echo "1. validate-only: sol-pig builds and registers"

# 2. A fake-provider session answers, with obs_recall and its snippet in the request.
cd "$HOME/cwd"
SESSION="$WORK/session.jsonl"
pig --offline --no-session --model test-faux/faux-1 --mode json -p 'What is 20+22?' > "$SESSION" 2> "$WORK/session.stderr" \
  || fail "pig session exited $? (see $WORK/session.stderr)"
grep -q '42' "$SESSION" || fail "the answer 42 is missing from $SESSION"
grep -q 'obs_recall' "$SESSION" || fail "obs_recall is not in the request (see $SESSION)"
grep -q 'Recall a paged excerpt from a previously replaced large tool result' "$SESSION" || fail "obs_recall's prompt snippet is missing"
echo "2. fake-provider session: answered 42; obs_recall and its snippet reached the request"

# 3. Idle footprint of one instance: pig plus the extension runner.
( sleep 6 ) | pig --offline --no-session --model test-faux/faux-1 --mode rpc > /dev/null 2>&1 &
PIG_PID=$!
sleep 3
descendants() {
  ps -A -o pid=,ppid= | awk -v root="$1" '{pp[$1]=$2} END {s[root]=1; c=1; while (c) {c=0; for (p in pp) if (!(p in s) && (pp[p] in s)) {s[p]=1; c=1}} for (p in s) print p}'
}
TOTAL_KB=0; PROCS=0
for pid in $(descendants "$PIG_PID"); do
  if command -v footprint >/dev/null; then
    kb=$(footprint -p "$pid" --noCategories -f bytes 2>/dev/null | awk '/phys_footprint:/{print int($2/1024); exit}')
  else
    kb=$(ps -o rss= -p "$pid" 2>/dev/null | tr -d ' ')
  fi
  [ -n "${kb:-}" ] && { TOTAL_KB=$((TOTAL_KB + kb)); PROCS=$((PROCS + 1)); }
done
wait "$PIG_PID" 2>/dev/null || true
[ "$PROCS" -ge 2 ] || fail "expected pig and its extension runner, saw $PROCS process(es)"
echo "3. idle footprint: $((TOTAL_KB / 1024)) MiB across $PROCS processes (pig + extension runner)"

# 4. ObservationPack end to end against a scripted endpoint: full, full, placeholder, recall.
MOCK_LOG="$WORK/mock-log"
( cd "$ROOT/scripts/live/mockprovider" && exec go run . -addr 127.0.0.1:0 -log-dir "$MOCK_LOG" -scenario observation-pack ) \
  > "$WORK/mock.stdout" 2> "$WORK/mock.stderr" &
MOCK_PID=$!
trap 'kill $MOCK_PID 2>/dev/null' EXIT
for _ in $(seq 1 120); do [ -s "$MOCK_LOG/addr" ] && break; sleep 0.5; done
[ -s "$MOCK_LOG/addr" ] || fail "the scripted endpoint did not start (see $WORK/mock.stderr)"
ADDR=$(cat "$MOCK_LOG/addr")
cat > "$PIG_CODING_AGENT_DIR/models.json" <<JSON
{"providers":{"mock":{"baseUrl":"http://$ADDR/v1","api":"openai-completions","apiKey":"mock","models":[{"id":"mock-1","name":"Mock","contextWindow":128000,"maxTokens":4096}]}}}
JSON
cd "$HOME/cwd"
pig --offline --no-session --model mock/mock-1 --mode json -p 'Run the scripted scenario.' > "$WORK/scenario.jsonl" 2> "$WORK/scenario.stderr" \
  || fail "scripted session exited $? (see $WORK/scenario.stderr)"
grep -q 'All done\.' "$WORK/scenario.jsonl" || fail "the scripted session did not reach its final answer (see $WORK/scenario.jsonl)"
SUMMARIES="$MOCK_LOG/requests.jsonl"
AGENT=$(grep -c '"kind":"agent"' "$SUMMARIES" || true)
[ "$AGENT" -eq 5 ] || fail "expected 5 agent requests, saw $AGENT: $(cat "$SUMMARIES")"
agent_line() { grep '"kind":"agent"' "$SUMMARIES" | sed -n "${1}p"; }
first_bytes() { sed -E 's/.*"toolResultBytes":\[([0-9]+).*/\1/' <<<"$1"; }
L2=$(agent_line 2); L3=$(agent_line 3); L4=$(agent_line 4)
{ grep -q '"toolResults":1,' <<<"$L2" && [ "$(first_bytes "$L2")" -gt 10240 ] && grep -q '"placeholders":0' <<<"$L2"; } \
  || fail "request 2 should carry the full result: $L2"
grep -q '"placeholders":0' <<<"$L3" || fail "request 3 should still carry the full result: $L3"
{ grep -q '"placeholders":1' <<<"$L4" && [ "$(first_bytes "$L4")" -lt 2048 ]; } || fail "request 4 should carry the placeholder: $L4"
{ grep -q '\[obs_recall id=obs_' "$MOCK_LOG/request-005-agent.json" && grep -q 'chunk_bytes=' "$MOCK_LOG/request-005-agent.json"; } \
  || fail "request 5 lacks the obs_recall result"
LEDGER=$(ls -dt "$WORK"/tmp/sol-pi-* | head -1)/observation-pack/ledger.jsonl
[ -f "$LEDGER" ] || fail "no ObservationPack ledger under $WORK/tmp/sol-pi-*"
EVENTS=$(sed -E 's/.*"event":"([a-z]+)".*/\1/' "$LEDGER" | tr '\n' ' ')
case "$EVENTS" in "full full placeholder recall"*) ;; *) fail "ledger events: $EVENTS" ;; esac
grep -q '"sendNumber":3' "$LEDGER" || fail "the placeholder entry lacks sendNumber 3"
FIRST=$(sed -n 1p "$LEDGER")
OBJECT="$(dirname "$LEDGER")/objects/$(sed -E 's/.*"id":"(obs_[a-f0-9]{24})".*/\1/' <<<"$FIRST").txt"
ORIG=$(sed -E 's/.*"originalBytes":([0-9]+).*/\1/' <<<"$FIRST")
[ "$(wc -c < "$OBJECT" | tr -d ' ')" -eq "$ORIG" ] || fail "archived object $OBJECT does not hold the $ORIG original bytes"
kill "$MOCK_PID" 2>/dev/null; wait "$MOCK_PID" 2>/dev/null || true
echo "4. scripted endpoint: placeholder on the third send, obs_recall returned the bytes"

echo "verify.sh: ok"
