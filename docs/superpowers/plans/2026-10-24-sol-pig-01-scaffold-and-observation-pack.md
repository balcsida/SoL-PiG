# SoL-PiG plan 1: scaffold and ObservationPack

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go extension that PiG 0.5.3 loads as `sol-pig`, reads `sol-pi.json`, and runs SoL-Pi's ObservationPack with SoL-Pi's exact behaviour, plus the fake host and live check the later mechanisms build on.

**Architecture:** One Go module at `extensions/sol-pig` (standard library plus PiG's SDK). The factory registers only `session_start`; that handler loads the config once and registers the enabled mechanisms late, as SoL-Pi does. Pure logic lives in internal packages with SoL-Pi's tests ported to `go test`; host-facing code is exercised against `internal/testhost`, a fake PiG speaking the subprocess wire over `net.Pipe()`; the live check runs real `pig` with its built-in fake provider.

**Tech Stack:** Go ≥ 1.26, `github.com/MichaelKinsy/PiG/extensions/sdk v0.5.3`, `go test`, `pig` 0.5.3, bash, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-10-24-sol-pig-design.md` (design), `docs/superpowers/recon/2026-10-24-sol-pi-behaviour-spec.md` (behaviour spec; "spec §n" below refers to it), `docs/superpowers/recon/2026-10-24-pig-go-sdk.md` (SDK recon; line numbers there are from an older PiG commit, re-check against tag `v0.5.3`).

This is plan 1 of 6. Later plans: 2 scripted-endpoint live harness, 3 Action Fusion, 4 Evidence-Preserving Reducer, 5 Online Context Compact, 6 docs and release.

## Global Constraints

- Module `github.com/balcsida/SoL-PiG/extensions/sol-pig`, `go 1.26`, `require github.com/MichaelKinsy/PiG/extensions/sdk v0.5.3`, no `replace`, no other dependency.
- Extension identity `sol-pig`: directory `extensions/sol-pig`, `sdk.New("sol-pig")`, root `package.json` with `"pi":{"extensions":["extensions/sol-pig"]}`.
- Factory signature exactly `func Extension() *sdk.Extension` in package `solpig`.
- Never write to stdout; never call `os.Exit`, `os.Chdir`, `log.Fatal*`; stderr only for the fail-open lines named in the spec.
- Every name the model or a user sees comes from the behaviour spec verbatim: config keys and error messages (spec §0.2), tool `obs_recall` (spec §2.3), placeholder (spec §2.5), recall header (spec §2.3), ledger fields (spec §2.4), runtime root `sol-pi/<id>` and `sol-pi-<id>-` temp prefix (spec §0.4), status key `sol-pi-savings`. Banner and notification product name is `SoL-PiG` (design 5.2).
- All mechanisms off unless `sol-pi.json` enables them; a config error stops loading with the spec's message.
- Fail-open per tool result in ObservationPack (spec §2.4 step f); never alter a message whose archive cannot be verified.
- Files SoL-PiG creates: directories 0700, objects 0600, created with `O_EXCL`; never follow a symlink into them.
- Every file ported from SoL-Pi starts with `// Ported from SoL-Pi <path> at 2195680 (NVIDIA, MIT).` under the repository's own SPDX header.
- Commit with Conventional Commits; run `go vet ./... && go test ./...` in `extensions/sol-pig` before each commit.

## Review Focus

1. A tool result whose text puts a multi-byte character across the 512-byte excerpt budget or the recall chunk end: the excerpt must still be whole lines of valid UTF-8 and the chunk end trimmed to a character boundary (tests in Task 6).
2. `obs_recall` with an offset inside a multi-byte character: no panic, deterministic bytes, `next_offset` still advances (test in Task 6).
3. A `context` message list containing non-object entries, a tool result whose `content` is a string rather than blocks, or a block without `text`: skipped, never altered, no panic (test in Task 7).
4. `sol-pi.json` that exists but is a directory or unreadable: `Unable to read SoL-Pi config <path>: <reason>` (test in Task 3).
5. A session id that is safe but a session directory path with spaces and non-ASCII characters: the runtime root is created and reused (test in Task 4).

---

### Task 1: Module scaffold and factory

**Files:**
- Create: `package.json`, `.gitignore`, `LICENSE`, `THIRD_PARTY_NOTICES.md`, `UPSTREAM.md`, `README.md`
- Create: `extensions/sol-pig/go.mod`, `extensions/sol-pig/extension.go`
- Test: `extensions/sol-pig/extension_test.go`

**Interfaces:**
- Produces: `package solpig`; `func Extension() *sdk.Extension` returning `sdk.New("sol-pig")` with one `OnSessionStart` handler that, for now, returns `nil, nil`.

- [ ] **Step 1: Write the failing test**

```go
// extensions/sol-pig/extension_test.go
package solpig

func TestExtensionIdentity(t *testing.T) {
	if got := Extension().Name(); got != "sol-pig" {
		t.Fatalf("Name() = %q, want %q", got, "sol-pig")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd extensions/sol-pig && go test ./... -run TestExtensionIdentity`
Expected: FAIL (no `go.mod` / package yet).

- [ ] **Step 3: Create the module and factory**

`go.mod`: `module github.com/balcsida/SoL-PiG/extensions/sol-pig`, `go 1.26`, `require github.com/MichaelKinsy/PiG/extensions/sdk v0.5.3`; run `go mod tidy` (produces `go.sum`). `extension.go`: `func Extension() *sdk.Extension` as in Interfaces. `package.json`: `{"name":"sol-pig","version":"0.1.0","private":true,"pi":{"extensions":["extensions/sol-pig"]}}`. `.gitignore`: `.worktrees/`, `*.test`, `scripts/live/work/`. `LICENSE`: MIT with `Copyright (c) 2026 SoL-PiG contributors` and `Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.` (SoL-OpenCode's `LICENSE` is the model). `THIRD_PARTY_NOTICES.md`: SoL-Pi (NVIDIA, MIT, `https://github.com/NVlabs/SoL-Pi`, commit `2195680`), PiG (Hewlett Packard Enterprise Development LP, MIT, tag `v0.5.3`) and Pi (Mario Zechner, MIT) for code copied in later plans. `UPSTREAM.md`: a table `file | source | commit` with the SoL-Pi rows added as files are ported, starting empty except a header. `README.md`: one paragraph (what it is, SoL-Pi attribution, "install SoL-PiG or SoL-Pi in a PiG, not both") and the install line `pig install git:github.com/balcsida/SoL-PiG`.

- [ ] **Step 4: Run the test and the PiG validation**

Run: `cd extensions/sol-pig && go vet ./... && go test ./... -run TestExtensionIdentity`
Expected: PASS.

Run (from the repository root): `PIG_HOME=$(mktemp -d) PIG_OFFLINE=1 PI_TELEMETRY=0 pig install "$PWD" --validate-only --json`
Expected: JSON with `"valid": true`, `"name": "sol-pig"`, `"registered": true`, `"language": "go"`.

- [ ] **Step 5: Commit**

```bash
git add package.json .gitignore LICENSE THIRD_PARTY_NOTICES.md UPSTREAM.md README.md extensions/sol-pig
git commit -m "feat: scaffold the sol-pig Go extension"
```

### Task 2: Fake PiG host for tests

**Files:**
- Create: `extensions/sol-pig/internal/testhost/testhost.go`
- Test: `extensions/sol-pig/internal/testhost/testhost_test.go`

**Interfaces:**
- Consumes: the wire as PiG `v0.5.3` implements it: 4-byte big-endian length prefix then JSON; envelope `type` ∈ `register`, `ready`, `request`, `response`, `call`, `call_result`, `notify`, `request_state`, `widget_push`; requests carry `request.method` ∈ `tool_call`, `event`, `command`, …, with `tool`, `event`, `handler_id`, `args`. Reference implementation: PiG `examples/extensions/plan-mode/main_test.go` (`startExtensionHost`, `readEnvelope`, `writeEnvelope`, `invoke`, `handleCall`) and host-call method names in `extensions/sdk/context.go`, `extensions/sdk/session_manager_class.go`, `extensions/sdk/tool_definition.go` (`registerTool`), `extensions/sdk/extension.go` (`event.subscribe`).
- Produces:

```go
package testhost

type Options struct {
	Cwd, Mode      string            // Mode defaults to "tui"
	ProjectTrusted bool
	SessionDir     string            // "" means no persistent session (SoL-Pi temp fallback)
	SessionID      string
	Settings       map[string]any    // answer to getSettings
	ActiveTools    []string
}
type Call struct{ Method string; Args map[string]any }
type ToolDecl struct{ Name, Description, PromptSnippet string; PromptGuidelines []string; Parameters map[string]any }
type ToolCallResult struct{ Content []any; Details any; IsError bool; Err error }

func Start(t *testing.T, ext *sdk.Extension, opts Options) *Host     // handshake done on return
func (h *Host) RegisteredHandlers() map[string][]int                   // from the register frame only
func (h *Host) Handlers(event string) []int                            // register frame plus later event.subscribe, minus unsubscribes
func (h *Host) Tools() []ToolDecl                                      // register frame plus later registerTool calls, latest per name
func (h *Host) Emit(event string, data map[string]any) (any, error)    // every handler in order; returns the last non-nil result
func (h *Host) EmitContext(messages []any) (held []any, replaced bool) // Emit("context"); held is the list the host now holds; replaced is true when a handler returned a new list
func (h *Host) CallTool(name, toolCallID string, params map[string]any) ToolCallResult
func (h *Host) Calls(method string) []Call                             // recorded extension→host calls, in order
func (h *Host) Entries() []map[string]any                              // appendEntry results, served by getBranch as {"type":"custom","customType":…,"data":…}
func (h *Host) Handle(method string, fn func(args map[string]any) (any, error)) // scripted reply for a host method
```

Default replies: `getActiveTools` → `ActiveTools`; `notify`, `setStatus`, `event.subscribe`, `event.unsubscribe` → recorded, `null`; `registerTool` → recorded into `Tools()`, reply what the SDK expects (see `tool_definition.go`); session-manager calls answer `SessionDir`/`SessionID`/`Entries()`; `isProjectTrusted` → `ProjectTrusted`; `getSettings` → `Settings`. Any other method fails the test with `unhandled host call <method> <args>` unless `Handle` scripted it.

- [ ] **Step 1: Write the failing self-test**

```go
func TestHostDrivesAToolAndAnEvent(t *testing.T) {
	ext := sdk.New("probe")
	ext.Tool("echo", "echo", sdk.Schema{"type": "object"}, func(ctx sdk.Context, p map[string]any) (any, error) {
		ctx.Notify("hi", "info")
		return sdk.ToolResult{Content: "echo:" + p["text"].(string), Details: map[string]any{"n": 1}}, nil
	})
	seen := 0
	ext.OnEvent(sdk.EventContext, func(ctx sdk.Context, d map[string]any) (any, error) { seen++; return nil, nil })
	h := Start(t, ext, Options{Cwd: t.TempDir(), SessionDir: t.TempDir(), SessionID: "s1"})
	if got := h.Handlers("context"); len(got) != 1 { t.Fatalf("handlers = %v", got) }
	res := h.CallTool("echo", "call_1", map[string]any{"text": "x"})
	if res.Err != nil || res.Content[0].(map[string]any)["text"] != "echo:x" || res.Details.(map[string]any)["n"] != float64(1) {
		t.Fatalf("result = %+v", res)
	}
	if c := h.Calls("notify"); len(c) != 1 || c[0].Args["message"] != "hi" { t.Fatalf("notify calls = %+v", c) }
	h.Emit("context", map[string]any{"messages": []any{}})
	if seen != 1 { t.Fatalf("context handler ran %d times", seen) }
}

func TestHostRecordsLateRegistration(t *testing.T) {
	ext := sdk.New("probe")
	ext.OnSessionStart(func(ctx sdk.Context, d map[string]any) (any, error) {
		ext.OnEvent(sdk.EventToolResult, func(sdk.Context, map[string]any) (any, error) { return nil, nil })
		ext.RegisterTool(sdk.ToolDefinition{Name: "late", Description: "d", Parameters: sdk.Schema{"type": "object"}, Execute: func(sdk.Context, map[string]any) (any, error) { return "ok", nil }})
		return nil, nil
	})
	h := Start(t, ext, Options{Cwd: t.TempDir()})
	if len(h.RegisteredHandlers()["tool_result"]) != 0 { t.Fatal("tool_result subscribed before session_start") }
	h.Emit("session_start", map[string]any{})
	if len(h.Handlers("tool_result")) != 1 || len(h.Tools()) != 1 || h.Tools()[0].Name != "late" {
		t.Fatalf("late registration not recorded: %v %v", h.Handlers("tool_result"), h.Tools())
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/testhost/`
Expected: FAIL, `Start` undefined.

- [ ] **Step 3: Implement `testhost.go`**

Framing and handshake as in PiG's `plan-mode/main_test.go`; the `ready` frame carries `cwd`, `mode`, `width` 100, `height` 30 and `state{activeTools,isIdle:false,projectTrusted,hasUI:true}`. A background reader routes `call` envelopes to the default or scripted handler and `response` envelopes to the waiting `invoke`. `EmitContext` builds `{"messages": msgs}`, emits, and returns the replacement list with `replaced` true if a handler returned one, else the (mutated) input with `replaced` false. Read the SDK sources named above for the exact method names and reply shapes the SDK expects; do not guess them.

- [ ] **Step 4: Run the self-test**

Run: `go test ./internal/testhost/ -v`
Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add extensions/sol-pig/internal/testhost
git commit -m "test: add a fake PiG host speaking the subprocess wire"
```

### Task 3: Config loading (`sol-pi.json`)

**Files:**
- Create: `extensions/sol-pig/internal/config/config.go` (ported from `src/sol-pi/config.ts`)
- Test: `extensions/sol-pig/internal/config/config_test.go`

**Interfaces:**
- Produces:

```go
package config

type Config struct {
	Version                   int
	ActionFusion              bool
	ObservationPack           bool
	EvidencePreservingReducer bool
	OnlineContextCompact      bool
	ReducerProvider           string  // default "openai-codex"
	ReducerModel              string  // default "gpt-5.6-luna"
	CacheWriteReadRatio       float64 // default 12.5
}
const FileName = "sol-pi.json"
func Default() Config
// Load reads <cwd>/<projectDirName>/sol-pi.json when allowProject, else <agentDir>/sol-pi.json, else Default().
func Load(cwd, projectDirName, agentDir string, allowProject bool) (Config, error)
func Parse(path string, data []byte) (Config, error) // validation of one file's bytes
```

Validation order and messages exactly as spec §0.2 items 1–7, with `${path}` the file path; string values trimmed; unknown keys checked before `version`.

- [ ] **Step 1: Write the failing tests**

Table-driven `TestParse` with cases (name → input → want error substring or want Config):
`defaults` (`{"version":1}` → `Default()`); `ratio zero` (`0` accepted); `ratio 3.25`; `route trimmed` (`" openai "` → `"openai"`); `unknown key` (`{"version":1,"provider":"x"}` → `Unknown SoL-Pi config key: provider`); `version 2` → `SoL-Pi config version must be 1: <path>`; `actionFusion string` → `SoL-Pi config actionFusion must be boolean: <path>`; `ratio null`, `ratio string "12.5"`, `ratio -1` → `SoL-Pi config cacheWriteReadRatio must be a finite non-negative number: <path>`; `empty route` and `null route` → `SoL-Pi config evidencePreservingReducerModel must be a non-empty string: <path>`; `array` → `SoL-Pi config must be a JSON object: <path>`; `malformed` (`{`) → prefix `Unable to read SoL-Pi config <path>: `.
`TestLoad`: no files → `Default()`; only global → global; both and trusted → project replaces global entirely (global sets `actionFusion:true`, project sets only `observationPack:true` → `ActionFusion` false); both and untrusted → global; config path is a directory → error starts `Unable to read SoL-Pi config <path>: `.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/config/`
Expected: FAIL to compile.

- [ ] **Step 3: Implement `config.go`**

Decode with `json.Unmarshal` into `map[string]json.RawMessage` after checking the top-level token is an object; distinguish JSON `null` from absent with `json.RawMessage`; `math.IsInf`/`IsNaN` for the ratio.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add extensions/sol-pig/internal/config UPSTREAM.md
git commit -m "feat: load and validate sol-pi.json as SoL-Pi does"
```

(Add the `config.go ← src/sol-pi/config.ts @ 2195680` row to `UPSTREAM.md` in this and every later porting task.)

### Task 4: Paths: agent dir, project config dir, runtime root

**Files:**
- Create: `extensions/sol-pig/internal/paths/paths.go` (runtime root ported from `src/sol-pi/runtime-paths.ts`; directory rules from PiG `internal/codingagent/paths.go` at `v0.5.3`)
- Test: `extensions/sol-pig/internal/paths/paths_test.go`

**Interfaces:**
- Produces:

```go
package paths

// Getenv is os.Getenv in production; tests pass a map lookup.
func AgentDir(getenv func(string) string, home string) string
func ProjectConfigDirName(getenv func(string) string) string // ".pig", or ".pi" when PIG_USE_PI_DIRS=1
var ErrUnsafeSessionID = errors.New("SoL-Pi requires a safe Pi session id")
// RuntimeRoot returns <sessionDir>/sol-pi/<sessionID>, or for an empty sessionDir a per-process
// cached os.MkdirTemp(os.TempDir(), "sol-pi-"+sessionID+"-") created 0700. It does not create the
// non-temp root; callers mkdir what they write.
func RuntimeRoot(sessionDir, sessionID string) (string, error)
```

Rules (design 5.3): shared mode iff `PIG_USE_PI_DIRS=1`; agent dir = `PIG_CODING_AGENT_DIR` (default mode) or `PI_CODING_AGENT_DIR` (shared) when set, with a leading `~` expanded to `home`; else shared → `home/.pi/agent`; else `PIG_HOME/agent`, else `XDG_CONFIG_HOME/pig/agent`, else `home/.pig/agent`. Safe id: `^[a-z0-9][a-z0-9._-]*$` case-insensitive.

- [ ] **Step 1: Write the failing tests**

`TestAgentDir` table: `{}` → `home/.pig/agent`; `{PIG_HOME:"/p"}` → `/p/agent`; `{XDG_CONFIG_HOME:"/x"}` → `/x/pig/agent`; `{PIG_HOME:"/p",XDG_CONFIG_HOME:"/x"}` → `/p/agent`; `{PIG_CODING_AGENT_DIR:"~/a"}` → `home/a`; `{PIG_USE_PI_DIRS:"1"}` → `home/.pi/agent`; `{PIG_USE_PI_DIRS:"1",PI_CODING_AGENT_DIR:"/q"}` → `/q`; `{PIG_USE_PI_DIRS:"1",PIG_CODING_AGENT_DIR:"/ignored"}` → `home/.pi/agent`.
`TestProjectConfigDirName`: default `.pig`; shared `.pi`.
`TestRuntimeRoot`: persistent → `filepath.Join(dir, "sol-pi", "abc")` for a dir named `t.TempDir()+"/sés sion dir"`; in-memory → path under `os.TempDir()` whose base starts with `sol-pi-abc-`, mode 0700, same path on a second call, a different id gives a different path, the first dir still exists; unsafe ids `""`, `"."`, `".."`, `"../escape"`, `"nested/session"`, `"nested\\session"` → `ErrUnsafeSessionID` in both modes.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/paths/`
Expected: FAIL to compile.

- [ ] **Step 3: Implement `paths.go`**

A package-level `sync.Mutex` plus `map[string]string` caches temp roots by session id.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/paths/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add extensions/sol-pig/internal/paths UPSTREAM.md
git commit -m "feat: resolve PiG directories and the SoL-Pi runtime root"
```

### Task 5: Savings display

**Files:**
- Create: `extensions/sol-pig/internal/savings/savings.go` (ported from `src/sol-pi/tui.ts`)
- Test: `extensions/sol-pig/internal/savings/savings_test.go`

**Interfaces:**
- Produces:

```go
package savings

type Mechanism string
const (
	ActionFusion         Mechanism = "Action Fusion"
	ObservationPack      Mechanism = "Observation Pack"
	LunaDelegating       Mechanism = "Luna Delegating"
	OnlineContextCompact Mechanism = "Online Context Compact"
)
const StatusKey = "sol-pi-savings"
var StatusDuration = 4 * time.Second // tests shorten it
func FormatCount(v float64, unit string) string   // "12,345 context tokens avoided"
func FormatBytes(v float64) string                // "76.4 KiB removed from future prompts"
func BannerLines(m Mechanism, saving string, base []string) []string // "⚡ SoL-PiG · <m>", "Money saved · <saving>", base...
func IsNestedToolCallID(id string) bool           // /\/\d+$/
// Show does nothing unless ctx.Mode() == "tui"; then Notify("⚡ SoL-PiG · <m>\nMoney saved · <saving>", "info"),
// SetStatus(StatusKey, "⚡ <m> · <saving>"), and after StatusDuration SetStatus(StatusKey, "") unless a newer Show replaced it.
func Show(ctx sdk.Context, m Mechanism, saving string)
```

Formatting rules: spec §0.5 (`formatSavingsCount`, `formatSavingsBytes`: clamp at 0, round, en-US thousands separators; MiB/KiB/B thresholds; one decimal with trailing `.0` dropped).

- [ ] **Step 1: Write the failing tests**

`TestFormatCount`: `12345` → `12,345 context tokens avoided`; `-3` → `0 context tokens avoided`; `NaN` → `0 …`; `999.6` → `1,000 …`. `TestFormatBytes`: `78200` → `76.4 KiB removed from future prompts`; `1048576` → `1 MiB removed from future prompts`; `512` → `512 B removed from future prompts`. `TestBannerLines` → exactly `["⚡ SoL-PiG · Action Fusion", "Money saved · 1 model round-trip avoided", "row"]`. `TestIsNestedToolCallID`: true for `toolu_01AbC/1`, `cm1/2/12`; false for `toolu_01AbC`, `call_abc|fc_def`, `call-1:then_run`. `TestShow` (testhost, `Mode: "tui"`, `StatusDuration = 20*time.Millisecond`): a tool handler calling `Show(ctx, ObservationPack, "12 context tokens avoided")` yields `notify` with `message` `"⚡ SoL-PiG · Observation Pack\nMoney saved · 12 context tokens avoided"`, `level` `"info"`, and `setStatus` with `key` `sol-pi-savings`, `text` `"⚡ Observation Pack · 12 context tokens avoided"`; after 100 ms a second `setStatus` with empty `text`. `TestShowSilentOutsideTUI`: `Mode: "rpc"` → no `notify`, no `setStatus`.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/savings/`
Expected: FAIL to compile.

- [ ] **Step 3: Implement `savings.go`**

The clear timer is `time.AfterFunc` guarded by a generation counter under a mutex so an older timer never clears a newer status.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/savings/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add extensions/sol-pig/internal/savings UPSTREAM.md
git commit -m "feat: show SoL savings in the TUI as SoL-Pi does"
```

### Task 6: ObservationPack core: observations, placeholder, storage, recall, ledger

**Files:**
- Create: `extensions/sol-pig/internal/observationpack/observation.go`, `ledger.go` (ported from `src/sol-pi/extensions/observation-pack/{observation,ledger}.ts`)
- Test: `extensions/sol-pig/internal/observationpack/observation_test.go`, `ledger_test.go`

**Interfaces:**
- Produces:

```go
package observationpack

const (
	ThresholdBytes         = 10 * 1024
	FullSends              = 2
	PlaceholderExcerptBytes = 1024 // 512 head, 512 tail
	RecallMaxBytes         = 16 * 1024
	RecallMaxLines         = 400
	RecallHeaderReserve    = 512
	RecallHeaderLines      = 2
	ReceiptPrefixLine      = "sol_pi_evidence_receipt_v1"
)
type Observation struct {
	ID, ToolName, ToolCallID, Text, ContentHash string
	Bytes, Lines, Tokens int
}
func IsObservationID(id string) bool // ^obs_[a-f0-9]{24}$
// NewObservation joins text blocks with "\n"; ok=false when any line equals ReceiptPrefixLine or Bytes <= ThresholdBytes.
func NewObservation(toolName, toolCallID string, textBlocks []string) (o Observation, ok bool)
func CountLines(text string) int                 // 0 for "", else count of "\n" plus 1 if no trailing newline
func EstimateTokens(text string) int             // ceil(UTF-16 code units / 4), SoL-Pi's JS .length semantics
func Placeholder(o Observation) string           // spec §2.5, lines joined with "\n"
func CompleteLineExcerpt(text string, budget int, fromEnd bool) string
func ObjectPath(root, id string) string           // <root>/observation-pack/objects/<id>.txt
func EnsureStored(root string, o Observation) error
type Chunk struct{ Text string; Bytes, Lines int; NextOffset int64; EOF bool }
func ReadRecallChunk(path string, offset int64) (Chunk, error) // maxBytes 15872, maxLines 398, UTF-8-safe end
type Ledger struct{ Path string }
func (l Ledger) Append(entry map[string]any) error // {"timestamp": RFC3339Nano UTC, ...entry} as one JSON line
```

Error texts (spec §2.3–2.4): `Observation directory is not a regular directory for <id>`, `Content-addressed observation is not a regular file for <id>`, `Content-addressed observation size mismatch for <id>`, `Content-addressed observation hash mismatch for <id>`, `Stored observation is not a regular file`, `Offset <n> exceeds observation size <size>`. `EnsureStored`: `os.MkdirAll(dir, 0o700)`, `Lstat` must be a directory and not a symlink, open `O_WRONLY|O_CREATE|O_EXCL` 0600 (plus `syscall.O_NOFOLLOW` on Unix), on `EEXIST` open read-only, require regular file, equal size, equal sha256. ID = `"obs_" + hex(sha256(toolName+"\x00"+toolCallID+"\x00"+contentHash))[:24]`, `ContentHash = hex(sha256(text))`.

- [ ] **Step 1: Write the failing tests**

`TestNewObservationThreshold`: 10240 ASCII bytes → ok=false; 10241 → ok=true with `Bytes` 10241, `Lines` counted, `ID` matching `IsObservationID`, same input twice → same ID, different `ToolCallID` → different ID. `TestNewObservationSkipsReceipts`: a block containing a line exactly `sol_pi_evidence_receipt_v1` → ok=false even above threshold. `TestPlaceholderFormat`: for a 20,000-byte text of numbered lines, the placeholder equals the exact 13-line template of spec §2.5 with `original_bytes`, `original_lines`, `estimated_tokens` filled, the head excerpt being the first whole lines within 512 bytes, the tail the last whole lines within 512 bytes, and `[<Bytes> original bytes omitted]`; calling twice gives identical bytes. `TestCompleteLineExcerpt`: lines of 100 bytes with budget 512 → 5 lines from the start / 5 from the end; a single 600-byte line → `""`; a multi-byte line crossing the budget is excluded whole (Review Focus 1). `TestEnsureStored`: writes object 0600 under dir 0700; second call with same content succeeds; same ID with different bytes of the same length → `hash mismatch`; different length → `size mismatch`; a symlink at the object path → error (not followed); a symlink as the objects directory → `not a regular directory` and nothing written through it. `TestReadRecallChunkPaging`: a 40 KiB UTF-8 text with multi-byte characters; following `NextOffset` from 0 until `EOF` concatenates to the exact original bytes; each chunk ≤ 15872 bytes and ≤ 398 lines and valid UTF-8; offset inside a multi-byte character returns a chunk and advances (Review Focus 2); offset > size → `Offset … exceeds observation size …`. `TestLedgerAppend`: two appends produce two JSON lines, each with `timestamp` first and the entry's fields; the parent directory is created.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/observationpack/`
Expected: FAIL to compile.

- [ ] **Step 3: Implement `observation.go` and `ledger.go`**

Port line by line from `observation.ts` (`createObservation`, `ensureStored`, `placeholderFor`, `completeLineExcerpt`, `readRecallChunk`, `trimUtf8End`) and `ledger.ts`; `EstimateTokens` counts `utf16` code units; `CompleteLineExcerpt` splits keeping `\n` with each line (JS `split(/(?<=\n)/)`).

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/observationpack/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add extensions/sol-pig/internal/observationpack UPSTREAM.md
git commit -m "feat: port ObservationPack's archive, placeholder, recall and ledger"
```

### Task 7: ObservationPack registration: `context` hook and `obs_recall`

**Files:**
- Create: `extensions/sol-pig/internal/observationpack/pack.go` (ported from `src/sol-pi/extensions/observation-pack/index.ts`)
- Test: `extensions/sol-pig/internal/observationpack/pack_test.go`

**Interfaces:**
- Consumes: Task 4 `paths.RuntimeRoot`; Task 5 `savings.Show`, `savings.BannerLines`, `savings.FormatCount`; Task 6.
- Produces:

```go
// Register adds the obs_recall tool, then the context handler. Safe to call once per extension.
func Register(ext *sdk.Extension)
```

Behaviour: spec §2.1–2.4. `context` handler: `root, err := runtimeRoot(ctx)` outside the per-message recovery (an error returns it); request index = assistant messages + 1; `priorAssistantCounts[i]`; a message participates when `role == "toolResult"`, `isError` not true, `content` is a non-empty `[]any` of maps each with `type == "text"` and a string `text`; otherwise it is skipped (Review Focus 3). Per message: observation → `EnsureStored` → send count (`sentCounts[root+"\x00"+id]`, falling back to the later-assistant count) → below `FullSends`: ledger `full` entry and increment; else ledger `placeholder` entry, `savings.Show(ctx, savings.ObservationPack, savings.FormatCount(removedTokens, "context tokens avoided"))` only when `previousSends == FullSends`, then replace `message["content"]` with `[]any{map[string]any{"type":"text","text":placeholder}}` in place, increment. Any error in the per-message block: `fmt.Fprintf(os.Stderr, "[observationpack] fail-open for tool result: %s\n", err)` and the message untouched. Handler returns `nil, nil`. Ledger entry fields exactly as spec §2.4 d–e. `obs_recall` tool: spec §2.3 verbatim (`Label` `Recall Observation`, `RenderShell` self, parameters `id` string with description `Observation id from a placeholder`, `offset` integer minimum 0 `Byte offset, default 0`); execute returns `sdk.ToolResult{Content: header + "\n" + chunk.Text, Details: map[string]any{"id","offset","bytes","lines","nextOffset","eof"}}`; errors as `error` values with the spec's texts; `RenderCall` → `savings.BannerLines(ObservationPack, "full observation replay avoided", []string{"Recall <id> from byte <offset>"})`; `RenderResult` → same banner with `Recalling the requested slice...` while partial else `Recalled <bytes> bytes across <lines> lines`.

- [ ] **Step 1: Write the failing tests** (all through `testhost.Start` with `Mode: "tui"`, `SessionDir: t.TempDir()`, `SessionID: "sess1"`, an extension built as `ext := sdk.New("sol-pig"); Register(ext)`)

Helpers: `bigResult(tool, callID string, n int)` builds a toolResult message map with `n` numbered lines (> 10 KiB); `conversation(msgs ...map[string]any) []any`; `assistant()` a minimal assistant message.
`TestRegistersToolAndContextHandler`: `h.Tools()` names == `["obs_recall"]`; `h.Handlers("context")` has 1 entry; no other events.
`TestFullTwiceThenPlaceholder`: emit context with `[user, bigResult]` then with `[user, bigResult, assistant]`: content unchanged both times; third emit `[…, assistant, assistant]` → content is one text block starting `[large tool result replaced after its first 2 provider requests]` containing `id: obs_`, the first and last lines of the original, and `[<bytes> original bytes omitted]`; fourth emit → byte-identical placeholder; the object file holds the exact original text; `h.Calls("notify")` has exactly one entry with message `"⚡ SoL-PiG · Observation Pack\nMoney saved · <n> context tokens avoided"` where `n = Tokens − placeholderTokens`; the ledger has entries `full, full, placeholder, placeholder` with `request` 1,2,3,4 and `sendNumber` 3,4 on the placeholders.
`TestInPlaceEditKeepsSystemMessagePositions`: a list `[system, user, bigResult, assistant, assistant, system]` → after the third send the returned list has the same length and the system messages at the same indexes, and `EmitContext` returns `replaced == false`.
`TestRestartReconstructsSendCount`: a fresh `Register` on a new extension against the same `SessionDir`/`SessionID` with a conversation that already has two later assistants → placeholder on its first emit.
`TestPerSessionIsolation`: two hosts with different `SessionID` → separate object dirs and independent counts.
`TestFailOpenOnHashMismatch`: pre-create the object file with different content of equal length → message untouched on every send and stderr (captured by swapping `os.Stderr` for a pipe) contains `[observationpack] fail-open for tool result:` and `hash mismatch`.
`TestPassThrough`: error results, text+image content, results carrying a `sol_pi_evidence_receipt_v1` line, a string `content`, a non-map entry, and a block without `text` are left unchanged on the third send and no object is written.
`TestRecallTool`: after a placeholder, `h.CallTool("obs_recall", "c1", {"id": id})` returns content starting `[obs_recall id=<id> offset=0 next_offset=<n> eof=<bool>]\n[chunk_bytes=<b> chunk_lines=<l>; use next_offset to continue]\n`, following `next_offset` reproduces the original bytes, `Details` has the six keys, and the ledger gains `recall` entries; `{"id":"nope"}` → `Err` message `Unknown observation id: nope`; a valid id with no file → the same message; offset past the end → `Offset … exceeds observation size …`.
`TestRecallRenderers`: `RenderCall` lines == `["⚡ SoL-PiG · Observation Pack", "Money saved · full observation replay avoided", "Recall obs_… from byte 0"]`.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/observationpack/ -run 'Registers|Full|InPlace|Restart|PerSession|FailOpen|PassThrough|Recall'`
Expected: FAIL, `Register` undefined.

- [ ] **Step 3: Implement `pack.go`**

State in a struct created by `Register` (mutex, `sentCounts map[string]int`, `ledgers map[string]Ledger`). `runtimeRoot(ctx)` = `paths.RuntimeRoot` of `ctx.SessionManager().GetSessionDir()` and `GetSessionID()`.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/observationpack/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add extensions/sol-pig/internal/observationpack UPSTREAM.md
git commit -m "feat: register ObservationPack's context hook and obs_recall"
```

### Task 8: Lazy registration from `session_start`

**Files:**
- Modify: `extensions/sol-pig/extension.go`
- Test: `extensions/sol-pig/extension_test.go`

**Interfaces:**
- Consumes: `config.Load`, `paths.AgentDir`, `paths.ProjectConfigDirName`, `observationpack.Register`.
- Produces: `func Extension() *sdk.Extension`; unexported `func registerConfigured(ext *sdk.Extension, cfg config.Config)` that registers ObservationPack when `cfg.ObservationPack` (later plans add the other three in SoL-Pi's order: Action Fusion, ObservationPack, Reducer, OCC).

Behaviour (spec §0.1): the handler runs once per extension instance (`sync.Once`-like flag set before loading, so a failure is not retried); `cfg, err := config.Load(ctx.Cwd(), paths.ProjectConfigDirName(os.Getenv), paths.AgentDir(os.Getenv, home), trusted)` with `trusted, _ := ctx.IsProjectTrusted()` and `home` from `os.UserHomeDir()`; on error return `nil, err`.

- [ ] **Step 1: Write the failing tests**

`TestRegisterFrameHasOnlySessionStart`: `h.RegisteredHandlers()` has exactly one event, `session_start`; `h.Tools()` empty.
`TestSessionStartRegistersEnabledMechanisms`: `t.Setenv("PIG_CODING_AGENT_DIR", dir)` with `dir/sol-pi.json` = `{"version":1,"observationPack":true}`; after `h.Emit("session_start", …)`: `Tools()` names `["obs_recall"]`, `Handlers("context")` length 1; a second `session_start` adds nothing.
`TestSessionStartAllDisabled`: `{"version":1}` → no tools, no handlers beyond `session_start`.
`TestSessionStartConfigError`: `{"version":1,"bogus":true}` → `Emit` returns an error containing `Unknown SoL-Pi config key: bogus` and nothing is registered.
`TestProjectConfigNeedsTrust`: `Options{ProjectTrusted: false, Cwd: cwd}` with `cwd/.pig/sol-pi.json` enabling ObservationPack and no global file → nothing registered; `ProjectTrusted: true` → `obs_recall` registered.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test . -v`
Expected: the new tests FAIL (no registration happens).

- [ ] **Step 3: Implement the handler in `extension.go`**

- [ ] **Step 4: Run all tests and the PiG validation**

Run: `go vet ./... && go test ./...`
Expected: PASS.
Run (repository root): `PIG_HOME=$(mktemp -d) PIG_OFFLINE=1 PI_TELEMETRY=0 pig install "$PWD" --validate-only --json`
Expected: `"valid": true`, `"handlers": ["session_start"]`.

- [ ] **Step 5: Commit**

```bash
git add extensions/sol-pig
git commit -m "feat: register enabled mechanisms once from session_start"
```

### Task 9: Live check and CI

**Files:**
- Create: `scripts/live/verify.sh`, `.github/workflows/ci.yml`
- Modify: `README.md` (Development section with the three commands)

**Interfaces:**
- Consumes: `pig` ≥ 0.5.3 on `PATH`; PiG's fake provider `PIG_TEST_FAUX=1`, `PIG_TEST_FAUX_SCENARIO=parity-basic`, model `test-faux/faux-1`.

`verify.sh` (modelled on SoL-OpenCode's `scripts/live/verify.sh`): creates `scripts/live/work/<timestamp>/` with `home/{cwd,pig/agent,cache,config,data}`; exports `HOME`, `PIG_HOME=home/pig`, `PIG_CODING_AGENT_DIR=home/pig/agent`, `XDG_*`, `TMPDIR`, `PIG_OFFLINE=1 PI_OFFLINE=1 PI_TELEMETRY=0 NO_COLOR=1 TERM=dumb`; writes `home/pig/agent/settings.json` = `{"packages":["<repo root>"]}` and `sol-pi.json` enabling `observationPack`; then asserts, failing with a message on each miss:
1. `pig install "<repo root>" --validate-only --json` → `valid` true, name `sol-pig`.
2. `pig --offline --no-session --model test-faux/faux-1 --mode json -p 'What is 20+22?'` exits 0, output contains `42`, `obs_recall` and `Recall a paged excerpt from a previously replaced large tool result`.
3. Idle footprint: start `pig … --mode rpc` with stdin held open for 6 s, after 3 s sum `footprint`/`ps -o rss` over pig and its descendants, print `idle footprint: <n> MiB`, then stop it.
Prints `verify.sh: ok` at the end.

`ci.yml`: on push and pull_request; `ubuntu-latest`; `actions/setup-go` with `go-version-file: extensions/sol-pig/go.mod`; `go vet ./... && go test ./...` in `extensions/sol-pig`; `actions/setup-node` (22) and `npm install -g @pi-in-go/pig@0.5.3`; `scripts/live/verify.sh` (step 3 prints with `ps` when `footprint` is absent).

- [ ] **Step 1: Write `verify.sh` and run it before the workflow exists**

Run: `scripts/live/verify.sh`
Expected: `verify.sh: ok` with the three assertions printed.

- [ ] **Step 2: Add `ci.yml`, update `README.md`, commit and push**

```bash
git add scripts/live/verify.sh .github/workflows/ci.yml README.md
git commit -m "ci: run go tests and the zero-spend live check"
git push
```

- [ ] **Step 3: Verify the workflow**

Run: `gh run watch --exit-status` (or `gh run list --limit 1`)
Expected: the `ci` run succeeds. If the npm package name or the `pig` version resolution differs on CI, fix the workflow, not the check.

---

## Self-review

- Spec coverage: design 5.1 (Task 1), 5.2 names (Tasks 3–7 constants), 5.3 lazy registration, directories, savings, renderers, logging (Tasks 4, 5, 7, 8), 5.5 ObservationPack (Tasks 6–7), 5.8 unit tests, fake host, live check, CI (Tasks 2, 9). 5.4, 5.6, 5.7 and the scripted endpoint of 5.8 are later plans by design.
- Types: `Observation`, `Chunk`, `Ledger`, `testhost.Host` methods, `savings.Mechanism` are used with the same names in Tasks 5–8.
- Review Focus items 1–5 are pinned in Tasks 6, 6, 7, 3, 4.
