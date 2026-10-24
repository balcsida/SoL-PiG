# SoL-PiG plan 2: scripted-endpoint live harness

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A scripted local OpenAI-compatible endpoint that drives real PiG through tool calls PiG's fake provider cannot make, and a live check that proves ObservationPack end to end on PiG 0.5.3: the third send of a large tool result is a placeholder, and `obs_recall` brings the bytes back.

**Architecture:** `scripts/live/mockprovider` is a small Go program in its own module (standard library only, outside `extensions/`, so PiG's discovery never sees it). It serves streaming Chat Completions, answers each agent request with the next step of a named scenario (a tool call, a text, or a step computed from the request), and logs every request body next to a one-line summary. `scripts/live/verify.sh` starts it on a loopback port, registers it through `models.json`, runs `pig -p` against `mock/mock-1`, and asserts from the log and the ledger. The design is `docs/superpowers/specs/2026-10-24-sol-pig-design.md` §5.8; the model is SoL-OpenCode's `scripts/live/mock-provider.mjs`.

**Tech Stack:** Go ≥ 1.26 (`net/http`, `net/http/httptest`, `encoding/json`), bash, `pig` 0.5.3.

**Spec:** `docs/superpowers/specs/2026-10-24-sol-pig-design.md` (§5.8 item 3); behaviour spec `docs/superpowers/recon/2026-10-24-sol-pi-behaviour-spec.md` §2 for what the placeholder and recall look like.

## Global Constraints

- The endpoint binds `127.0.0.1` only and never contacts anything else; no credential is involved (`apiKey` is the literal `mock`).
- Module `github.com/balcsida/SoL-PiG/scripts/live/mockprovider`, `go 1.26`, no dependencies, no `replace`.
- Request logs go only under the directory `-log-dir` names; `scripts/live/work/` stays git-ignored.
- Wire shapes are those of OpenAI Chat Completions streaming, exactly as SoL-OpenCode's `mock-provider.mjs` emits them: `chat.completion.chunk` objects, a tool call as one `tool_calls` delta with `index` 0 and `type` `function` followed by a chunk with `finish_reason` `tool_calls` and `usage`; a text as one `content` delta followed by `finish_reason` `stop` and `usage`; then `data: [DONE]`.
- The summary line per request has a fixed field order (`request, kind, bytes, messages, toolResults, toolResultBytes, placeholders, maxTokens`), so shell assertions can match on it.
- Nothing in `extensions/sol-pig` changes in this plan.
- Commit with Conventional Commits; `go vet ./... && go test ./...` in `scripts/live/mockprovider` and `scripts/live/verify.sh` green before the final commit.

## Review Focus

1. A request whose last message carries `content` as an array of parts rather than a string (Pi sends parts): classified and summarized the same way (test in Task 1).
2. A `tool` message whose `content` is an array: its bytes counted from the parts' `text` (test in Task 1).
3. The recall step when the request holds no placeholder: it must still answer (`id` `missing`) rather than hang the agent (test in Task 1).
4. A scenario that runs out of steps: the last step repeats, so the agent always reaches a text answer and the session ends (test in Task 1).
5. A request with `stream` false or missing: a `400` with a JSON error naming the cause, never a hang (test in Task 1).

---

### Task 1: The scripted endpoint

**Files:**
- Create: `scripts/live/mockprovider/go.mod`, `scripts/live/mockprovider/main.go`, `scripts/live/mockprovider/scenario.go`
- Test: `scripts/live/mockprovider/main_test.go`

**Interfaces:**
- Produces (package `main`):

```go
// A step is what the "model" does on one agent request.
type step struct {
	kind string          // "tool", "text" or "dynamic"
	name string          // tool name for "tool"
	args map[string]any  // tool arguments for "tool"
	text string          // reply for "text"
	build func(serializedRequest string) step // for "dynamic"
}
func toolCall(name string, args map[string]any) step
func textReply(value string) step
var recallObservation step // dynamic: obs_recall with the first `obs_[a-f0-9]{24}` in the request, or "missing"
var scenarios = map[string][]step{
	"observation-pack": {
		toolCall("bash", map[string]any{"command": "yes 'abcdefghijklmnopqrstuvwxyz 0123456789 padding padding' | head -n 500"}),
		toolCall("bash", map[string]any{"command": "echo second-step"}),
		toolCall("bash", map[string]any{"command": "echo third-step"}),
		recallObservation,
		textReply("All done."),
	},
}

type request struct { // the fields of a Chat Completions body the server reads
	Model    string           `json:"model"`
	Stream   bool             `json:"stream"`
	Messages []map[string]any `json:"messages"`
	Tools    []any            `json:"tools"`
	MaxTokens int             `json:"max_tokens"`
	MaxCompletionTokens int   `json:"max_completion_tokens"`
}
type summary struct {
	Request         int    `json:"request"`
	Kind            string `json:"kind"`
	Bytes           int    `json:"bytes"`
	Messages        int    `json:"messages"`
	ToolResults     int    `json:"toolResults"`
	ToolResultBytes []int  `json:"toolResultBytes"`
	Placeholders    int    `json:"placeholders"`
	MaxTokens       int    `json:"maxTokens"`
}
func messageText(m map[string]any) string      // string content, or the parts' "text" joined with "\n"
func classify(r request) string                // "agent" when len(Tools) > 0, else "auxiliary"
func summarize(n int, kind string, body []byte, r request) summary // placeholders = count of "[large tool result replaced" in body

type server struct { scenario []step; logDir string; mu sync.Mutex; requests, agentStep int }
func (s *server) ServeHTTP(w http.ResponseWriter, req *http.Request)
```

Behaviour: `GET` → `200 {"object":"list","data":[{"id":"mock-1","object":"model"}]}`. `POST` → read the body; `400 {"error":"invalid JSON"}` on a decode error; `400 {"error":"stream only"}` when `Stream` is false; otherwise increment `requests`, write `request-<NNN>-<kind>.json` (indented body) and append the summary line to `requests.jsonl`; `auxiliary` → the text `Scripted session`; `agent` → the step at `min(agentStep, len-1)`, then `agentStep++`; a `dynamic` step is built from the request body as a string; stream the reply with the chunk shapes in Global Constraints (`id` `chatcmpl-<n>`, `created` 0, `model` from the request, tool call `id` `call_<n>`, usage `prompt_tokens` = ceil(len(messages JSON)/4), `completion_tokens` 20). `main`: flags `-addr` (default `127.0.0.1:0`), `-log-dir` (default `./mock-log`), `-scenario` (default `observation-pack`, unknown → exit 2 with the known names); listen, write the bound address to `<log-dir>/addr`, print `mock provider on <addr> (<scenario>)` to stdout, serve until killed.

- [ ] **Step 1: Write the failing tests**

```go
func TestClassifyAndSummarize(t *testing.T) // Review Focus 1, 2
// parts content: {"role":"user","content":[{"type":"text","text":"hi"}]} → messageText "hi"; with Tools → "agent"; without → "auxiliary"
// tool message with parts: toolResultBytes[0] == len("abc") for content [{"type":"text","text":"abc"}]; a 12,000-byte string content → 12000
// placeholders counts "[large tool result replaced" occurrences in the raw body

func TestScenarioProgressionAndDynamicRecall(t *testing.T) // Review Focus 3, 4
// a server with scenarios["observation-pack"]: steps 1–3 return the three bash calls in order;
// step 4 with a body containing "id: obs_0123456789abcdef01234567" → obs_recall {"id":"obs_0123456789abcdef01234567","offset":0};
// step 4 with no placeholder → {"id":"missing","offset":0};
// step 5 and every later call → the text "All done."

func TestServeStreamsToolCallAndText(t *testing.T) // httptest.NewServer(s)
// POST {"model":"mock-1","stream":true,"messages":[{"role":"user","content":"go"}],"tools":[{}]} →
//   200, content-type text/event-stream, data lines parse as chunks: first has choices[0].delta.tool_calls[0].function.name == "bash"
//   and arguments JSON-equal to the step's args; second has finish_reason "tool_calls" and usage; last line is "data: [DONE]"
// a text step → first chunk delta.content == "All done.", second finish_reason "stop"
// files: <logDir>/request-001-agent.json exists and decodes; requests.jsonl line 1 starts with {"request":1,"kind":"agent",

func TestServeRejectsNonStream(t *testing.T) // Review Focus 5
// {"stream":false,...} → 400 and body {"error":"stream only"}; a non-JSON body → 400 {"error":"invalid JSON"}; GET → 200 with the model list
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd scripts/live/mockprovider && go test ./...`
Expected: FAIL to compile (no `go.mod`/symbols).

- [ ] **Step 3: Create the module, `scenario.go` and `main.go`**

Keep `ServeHTTP` free of global state so tests can build a `server` directly; `main` only parses flags and wires the listener.

- [ ] **Step 4: Run the tests**

Run: `cd scripts/live/mockprovider && go vet ./... && go test ./... -v`
Expected: all four PASS.

- [ ] **Step 5: Commit**

```bash
git add scripts/live/mockprovider
git commit -m "test(live): add a scripted OpenAI-compatible endpoint for zero-spend checks"
```

### Task 2: ObservationPack end to end in `verify.sh`

**Files:**
- Modify: `scripts/live/verify.sh` (add step 4 after the footprint step; keep steps 1–3 as they are)
- Modify: `README.md` (Development: one line saying the live check also runs a scripted endpoint)

**Interfaces:**
- Consumes: Task 1's binary via `go run ./scripts/live/mockprovider -addr 127.0.0.1:0 -log-dir "$WORK/mock-log" -scenario observation-pack`, its `addr` file, `requests.jsonl` summaries and `request-NNN-agent.json` bodies; `pig --model mock/mock-1`.

Step 4 of `verify.sh`:
1. Start the endpoint in the background from `$ROOT` (with the real `GOPATH`/`GOCACHE`/`GOMODCACHE` already exported), remember its pid, `trap 'kill $MOCK_PID 2>/dev/null' EXIT`, and wait up to 30 s for `$WORK/mock-log/addr`; `fail` otherwise.
2. Write `$PIG_CODING_AGENT_DIR/models.json`:
   ```json
   {"providers":{"mock":{"baseUrl":"http://<addr>/v1","api":"openai-completions","apiKey":"mock",
     "models":[{"id":"mock-1","name":"Mock","contextWindow":128000,"maxTokens":4096}]}}}
   ```
3. In `$HOME/cwd`: `pig --offline --no-session --model mock/mock-1 --mode json -p 'Run the scripted scenario.' > "$WORK/scenario.jsonl" 2> "$WORK/scenario.stderr"`; `fail` on a non-zero exit, and `fail` unless `scenario.jsonl` contains `All done.`.
4. Assert from `$WORK/mock-log/requests.jsonl`, each a `fail` with the offending line:
   - exactly 5 lines whose `"kind":"agent"`;
   - line 2 matches `"toolResults":1,"toolResultBytes":[` with a first value `> 10240` (extract with `sed`) and `"placeholders":0`;
   - line 3 has `"placeholders":0`;
   - line 4 has `"placeholders":1` and its first `toolResultBytes` value `< 2048` (the big result is now the placeholder);
   - `request-005-agent.json` contains `[obs_recall id=obs_` and `chunk_bytes=`.
5. Assert the ledger: exactly one directory `$WORK/tmp/sol-pi-*`; its `observation-pack/ledger.jsonl` has, in order, events `full`, `full`, `placeholder` (`"sendNumber":3`), then at least one `recall`; the `objects/` directory holds one file whose size equals the `originalBytes` in the first ledger line.
6. Print `4. scripted endpoint: placeholder on the third send, obs_recall returned the bytes` and stop the endpoint.

- [ ] **Step 1: Add step 4 and run the whole check**

Run: `scripts/live/verify.sh`
Expected: lines 1–4 printed and `verify.sh: ok`. On the first run, read `$WORK/mock-log/requests.jsonl` once by eye and confirm the five summaries are what the assertions expect before trusting them.

- [ ] **Step 2: Prove the assertions bite**

Run: `PIG_CODING_AGENT_DIR=... ` is not needed; instead temporarily set `observationPack` to `false` in the `sol-pi.json` the script writes (edit the script, run, revert).
Expected: step 4 fails on the line-4 placeholder assertion. Revert the edit and run again: `verify.sh: ok`.

- [ ] **Step 3: Update the README line, commit, push, watch CI**

```bash
git add scripts/live/verify.sh README.md
git commit -m "test(live): prove ObservationPack end to end against a scripted endpoint"
git push
gh run watch --exit-status
```
Expected: the `ci` run succeeds (the endpoint builds with the CI Go toolchain; no network).

---

## Self-review

- Spec coverage: design §5.8 item 3's scripted endpoint (Task 1), its ObservationPack scenario and the "prints what the endpoint received per request" requirement (Task 2), and the plan-1 handoff note that the live check must exercise the context hook (Task 2). TUI mode is still not exercised by the live check; `savings.Show` is covered by the fake-host tests, and the TUI stays a plan-6 verification note.
- Interfaces: `summary` field order is relied on by Task 2's `sed`/`grep`; `addr` file name and `requests.jsonl` are the only contract between the two tasks.
- Review Focus 1–5 are pinned in Task 1's tests.
