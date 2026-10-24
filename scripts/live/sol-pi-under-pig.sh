#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 SoL-PiG contributors
# SPDX-License-Identifier: MIT
#
# Pre-check for the Online Context Compact port: PiG must run SoL-Pi's own
# TypeScript compaction sequence (abort, settle, native compaction, resume)
# against the scripted loopback endpoint (scripts/live/mockprovider, scenario
# occ). Zero spend; everything runs in a throwaway HOME.
#
# Usage: scripts/live/sol-pi-under-pig.sh [work-directory]
# Env:   SOL_PI_DIR  a SoL-Pi checkout with node_modules (default: the recon clone;
#                    if it is absent, NVlabs/SoL-Pi at 2195680 is cloned into the work directory)
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
WORK="${1:-$ROOT/scripts/live/work/sol-pi-under-pig-$(date +%Y%m%d-%H%M%S)}"
DEFAULT_SOL_PI_DIR=/var/folders/2c/l4z5zdl52bnbx215ffbnzb3m0000gq/T/sol-pig-recon-l6HeCf/sol-pi

fail() { echo "sol-pi-under-pig: FAIL: $*" >&2; exit 1; }
command -v pig >/dev/null || fail "pig is not installed"
command -v go >/dev/null || fail "go is not installed"
command -v node >/dev/null || { echo "sol-pi-under-pig: node >= 22.19 is required (PiG runs TypeScript extensions on it); none found" >&2; exit 2; }
NODE_VERSION=$(node --version)
IFS=. read -r NODE_MAJOR NODE_MINOR _ <<<"${NODE_VERSION#v}"
if [ "$NODE_MAJOR" -lt 22 ] || { [ "$NODE_MAJOR" -eq 22 ] && [ "$NODE_MINOR" -lt 19 ]; }; then
  echo "sol-pi-under-pig: node >= 22.19 is required, found $NODE_VERSION" >&2
  exit 2
fi
echo "PiG $(pig --version) · node $NODE_VERSION · work directory $WORK"

REAL_GOPATH=$(go env GOPATH); REAL_GOCACHE=$(go env GOCACHE); REAL_GOMODCACHE=$(go env GOMODCACHE)
mkdir -p "$WORK"/home/{cwd,pig/agent,cache,config,data} "$WORK/tmp"

SOL_PI_DIR="${SOL_PI_DIR:-$DEFAULT_SOL_PI_DIR}"
if [ ! -d "$SOL_PI_DIR" ]; then
  SOL_PI_DIR="$WORK/sol-pi"
  git clone --quiet https://github.com/NVlabs/SoL-Pi "$SOL_PI_DIR" || fail "cannot clone SoL-Pi"
  git -C "$SOL_PI_DIR" checkout --quiet 2195680 || fail "cannot check out SoL-Pi 2195680"
  ( cd "$SOL_PI_DIR" && npm ci --silent ) || fail "npm ci failed in $SOL_PI_DIR"
fi
[ -f "$SOL_PI_DIR/src/sol-pi/index.ts" ] || fail "$SOL_PI_DIR has no src/sol-pi/index.ts"

export HOME="$WORK/home" USERPROFILE="$WORK/home" PIG_HOME="$WORK/home/pig" PIG_CODING_AGENT_DIR="$WORK/home/pig/agent"
export XDG_CACHE_HOME="$WORK/home/cache" XDG_CONFIG_HOME="$WORK/home/config" XDG_DATA_HOME="$WORK/home/data" TMPDIR="$WORK/tmp"
export PIG_OFFLINE=1 PI_OFFLINE=1 PI_TELEMETRY=0 NO_COLOR=1 TERM=dumb LC_ALL=C
export GOPATH="$REAL_GOPATH" GOCACHE="$REAL_GOCACHE" GOMODCACHE="$REAL_GOMODCACHE"
unset PIG_USE_PI_DIRS PI_CODING_AGENT_DIR

printf '{"version":1,"onlineContextCompact":true,"cacheWriteReadRatio":12.5}\n' > "$PIG_CODING_AGENT_DIR/sol-pi.json"
# PiG's own threshold auto-compaction trips at the same window minus reserve and would run before SoL-Pi's boundary; only SoL-Pi may compact here.
printf '{"compaction":{"enabled":false}}\n' > "$PIG_CODING_AGENT_DIR/settings.json"

( cd "$ROOT/scripts/live/mockprovider" && go build -o "$WORK/mockprovider" . ) || fail "the scripted endpoint does not build"
MOCK_LOG="$WORK/mock-log"
rm -rf "$MOCK_LOG"
"$WORK/mockprovider" -addr 127.0.0.1:0 -log-dir "$MOCK_LOG" -scenario occ > "$WORK/mock.stdout" 2> "$WORK/mock.stderr" &
MOCK_PID=$!
trap 'kill $MOCK_PID 2>/dev/null || true' EXIT
for _ in $(seq 1 60); do [ -s "$MOCK_LOG/addr" ] && break; sleep 0.5; done
[ -s "$MOCK_LOG/addr" ] || fail "the scripted endpoint did not start (see $WORK/mock.stderr)"
ADDR=$(cat "$MOCK_LOG/addr")
# contextWindow 60000 makes window protection (five ~10k-token logs) decide the compaction deterministically.
cat > "$PIG_CODING_AGENT_DIR/models.json" <<JSON
{"providers":{"mock":{"baseUrl":"http://$ADDR/v1","api":"openai-completions","apiKey":"mock","models":[{"id":"mock-1","name":"Mock","contextWindow":60000,"maxTokens":4096}]}}}
JSON

cd "$HOME/cwd"
pig --offline --no-session --model mock/mock-1 --mode json -e "$SOL_PI_DIR/src/sol-pi/index.ts" -p 'Run the scripted scenario.' \
  > "$WORK/session.jsonl" 2> "$WORK/session.stderr" || fail "pig exited $? (see $WORK/session.stderr)"
grep -q 'All done\.' "$WORK/session.jsonl" || fail "the session did not reach its final answer (see $WORK/session.jsonl)"

SUMMARIES="$MOCK_LOG/requests.jsonl"
SUMMARY_COUNT=$(grep -c '"kind":"summary"' "$SUMMARIES" || true)
[ "$SUMMARY_COUNT" -eq 1 ] || fail "expected exactly one summary (compaction) request, saw $SUMMARY_COUNT: $(cat "$SUMMARIES")"
SUMMARY_NO=$(grep '"kind":"summary"' "$SUMMARIES" | sed -E 's/^\{"request":([0-9]+),.*/\1/')
msgs() { sed -E 's/.*"messages":([0-9]+),.*/\1/' <<<"$1"; }
BEFORE=$(grep '"kind":"agent"' "$SUMMARIES" | awk -v n="$SUMMARY_NO" -F'[:,]' '$2 < n' | tail -1)
AFTER=$(grep '"kind":"agent"' "$SUMMARIES" | awk -v n="$SUMMARY_NO" -F'[:,]' '$2 > n' | head -1)
[ -n "$BEFORE" ] || fail "no agent request before the compaction: $(cat "$SUMMARIES")"
[ -n "$AFTER" ] || fail "no agent request after the compaction (the resume turn never came): $(cat "$SUMMARIES")"
grep -q '"toolResults":6,' <<<"$BEFORE" || fail "the compaction did not follow the second update_plan (the request that drew it carries six tool results): $BEFORE"
[ "$(msgs "$AFTER")" -lt "$(msgs "$BEFORE")" ] || fail "the first request after the compaction is not shorter: before $BEFORE / after $AFTER"
AFTER_NO=$(sed -E 's/^\{"request":([0-9]+),.*/\1/' <<<"$AFTER")
grep -qF 'Online context compaction finished.' "$(printf '%s/request-%03d-agent.json' "$MOCK_LOG" "$AFTER_NO")" \
  || fail "request $AFTER_NO lacks the post-compaction reminder"
! grep -q 'agent_settled' "$WORK/session.stderr" || fail "agent_settled error in $WORK/session.stderr: $(grep agent_settled "$WORK/session.stderr")"

echo "sol-pi-under-pig: PiG runs SoL-Pi's compaction sequence"
