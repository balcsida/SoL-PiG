# SoL-PiG design: SoL-Pi's four mechanisms as a native Go extension for PiG

Date: 2026-10-24. Status: draft for approval.

Companion documents (same repository):

- `docs/superpowers/recon/2026-10-24-sol-pi-behaviour-spec.md` — SoL-Pi's behaviour at commit `2195680`, file:line, with every test as an acceptance line (§7). This is the specification the port must meet.
- `docs/superpowers/recon/2026-10-24-pig-go-sdk.md` — how PiG's Go extension SDK realizes each Pi API SoL-Pi uses, with file:line into PiG `3452432f`.

## 1. Intent

[SoL-Pi](https://github.com/NVlabs/SoL-Pi) (NVIDIA, MIT) is a Pi coding-agent extension with four opt-in token-efficiency mechanisms: Action Fusion, ObservationPack, the Evidence-Preserving Reducer, and Online Context Compact. [PiG](https://github.com/MichaelKinsy/PiG) is Pi rebuilt in Go. PiG already runs SoL-Pi unchanged, as a Node subprocess. Measured on macOS with PiG 0.5.3 and 10 idle instances: PiG alone 116 MiB, PiG + a minimal Go extension 202 MiB, PiG + SoL-Pi under Node 1,407 MiB. Each session start also spends about 0.6 CPU-seconds in Node against 0.07 for the Go extension.

The owner routinely runs more than ten PiG instances at once. SoL-PiG ports the four mechanisms to a Go extension so that each instance pays a few MiB instead of ~130 MiB, while the model-visible behaviour (tool schemas, placeholders, receipts, prompts) stays identical to SoL-Pi's.

## 2. Goals, non-goals, anti-goals

Goals:

1. All four mechanisms, behaving as SoL-Pi `2195680` does, judged by the acceptance list in the behaviour spec §7.
2. Idle footprint per PiG instance in the tens of MiB at most, not hundreds; session start cost close to a trivial Go extension.
3. Installable with stock PiG: `pig install git:github.com/balcsida/SoL-PiG`, or a local path; no PiG patch, no Node.
4. Opt-in and off by default, configured by the same `sol-pi.json` SoL-Pi reads, so documentation and existing configs carry over.
5. Zero-spend tests: everything verifiable without a model provider or credentials.
6. Code that PiG can later fuse into a Piglet Binary without change.

Non-goals (v0.1):

- A Piglet Binary or fused build. Keep the code fusion-compatible; do not ship one yet.
- Changes to PiG. A PiG-vs-Pi behaviour difference found on the way is reported to PiG, not worked around with a fork.
- Running on Pi itself. SoL-Pi covers Pi.
- New mechanisms, new configuration keys, price inference, or any behaviour SoL-Pi does not have.
- Windows beyond best effort (PiG calls its own Windows support a preview).
- Paid or credentialed verification. Every check runs against PiG's built-in fake provider or a scripted local endpoint.
- SoL-Pi's Node preflight script (`scripts/check-sol-pi-config.mjs`). Its checks are covered by the config package's tests and by `pig install --validate-only`; the preflight lines in the acceptance list are marked not applicable.

Anti-goals (a failure even if it "works"):

- Model-visible text that differs from SoL-Pi: tool names, parameter schemas, descriptions, prompt guidelines, placeholder format, receipt format, reminder messages. These are the research artefact; the owner wants SoL's measured savings, not a variant.
- Editing persisted session history. Like SoL-Pi: ObservationPack and OCC change only the outgoing request; the reducer replaces a tool result only with a verified receipt whose original is archived.
- Sending anything to a model that the user has not opted into (only the reducer calls a model, only when `evidencePreservingReducer` is true).
- Silent failure. SoL-Pi's fail-open rules apply where SoL-Pi has them (per message or per result, original left intact, reason logged); configuration errors stop loading with a clear message, as in SoL-Pi.

## 3. Constraints

- Go ≥ 1.26 (SDK floor); the owner's machine has Go 1.27. PiG builds the extension from source on first load with the user's Go toolchain and caches the binary under its config root. Keep the module standard-library only so the first build needs no network and the code stays fusable.
- PiG ≥ 0.5.3, SDK module `github.com/MichaelKinsy/PiG/extensions/sdk v0.5.3`, no `replace` (PiG substitutes the SDK staged by the running binary; verified with the released 0.5.3 on 2026-10-24). The tag `extensions/sdk/v0.5.3` is published and resolves from the Go module proxy (checked 2026-10-24), so `go test` works outside PiG without a `replace` or `go.work`.
- Source of truth for anything copied from PiG, and for re-checking the recon's file:line claims, is the PiG tag `v0.5.3` (the commit that shipped as 0.5.3). The recon was taken at `3452432f`, an older main whose version constant still says 0.4.1; the builder re-checks cited lines against `v0.5.3` when copying.
- PiG's async contract: an event handler must do its work before returning (no fire-and-forget goroutines that outlive the handler without an owner); a `Context` kept after its handler returns is a "retained" context with the lifetime rules in `docs/extension-authoring.md`.
- Wire frames are JSON, capped at 128 MiB; the `context` event carries the whole conversation on every model call, so handlers must not copy it more than once.
- Licensing: MIT. Code ported from SoL-Pi carries NVIDIA's copyright; code copied from PiG carries HPE's and, for files that port Pi, Mario Zechner's. `THIRD_PARTY_NOTICES.md` lists all three, following SoL-OpenCode's precedent.

## 4. Approaches considered

1. **Standalone Go extension in a new repository** (chosen). Written against PiG's Go SDK, loaded by stock `pig`, the same code fusable later. Costs: a second port to keep in sync with SoL-Pi; Go versions of the Pi helpers SoL-Pi borrows; PiG's own edit/write/bash tool code lives under `internal/` and must be copied.
2. **Build SoL into PiG itself.** Zero extension overhead, but SoL is not part of Pi, so it would need acceptance as a PiG "additive feature" (D-numbered, with a stock-or-Piglet disposition decided by PiG's maintainer), would land slowly, and would move at PiG's release pace.
3. **Keep SoL-Pi under Node and trim it.** Rejected: the ~106 MiB Node process per instance is mostly Pi's own modules that SoL-Pi imports, not SoL's code, so there is little to trim without rewriting anyway.

The owner chose 1 on 2026-10-24. Why: it captures nearly all the memory saving today (a minimal Go extension costs ~9 MiB per instance), needs nobody's approval, and keeps both fusing and upstreaming open.

A second fork of NVlabs/SoL-Pi is impossible (GitHub allows one fork per account, and SoL-OpenCode holds it), so this is a plain new repository with attribution, as SoL-OpenCode is in substance.

## 5. Decisions

### 5.1 Repository and layout

Validated with PiG 0.5.3 (`pig install <dir> --validate-only` reported the package valid, identity `sol-pig`, and a Go tool's `PromptSnippet` and `PromptGuidelines` reached the system prompt in a fake-provider session).

```
SoL-PiG/
  package.json                  {"name":"sol-pig","private":true,"pi":{"extensions":["extensions/sol-pig"]}}
  extensions/sol-pig/           Go module github.com/balcsida/SoL-PiG/extensions/sol-pig
    go.mod                      go 1.26; require github.com/MichaelKinsy/PiG/extensions/sdk v0.5.3
    extension.go                func Extension() *sdk.Extension — sdk.New("sol-pig"); registers only session_start
    internal/config             sol-pi.json loading and validation (SoL-Pi config.ts)
    internal/paths              agent dir, project config dir, runtime root (SoL-Pi runtime-paths.ts + PiG paths rules)
    internal/savings            banner lines, notify/status, formatters (SoL-Pi tui.ts)
    internal/actionfusion       edit/write overrides, then_run, path resolution, per-path queue
    internal/pitools            edit/write implementations copied from PiG (attributed), see 5.4
    internal/observationpack    observation, ledger, placeholder, recall, context hook
    internal/reducer            candidate, archive, receipt, provider call, journal, tool_result hook
    internal/occ                economics, plan, state, projection, update_plan tool, event choreography
    internal/pisupport          estimateTokens, findCutPoint, session projection helpers (copied/ported, attributed)
    internal/testhost           fake PiG host over net.Pipe for tests (see 5.7)
  docs/configuration.md         schema, defaults, search order (PiG paths)
  docs/compatibility.md         PiG APIs used; every behaviour difference from SoL-Pi, with the reason
  docs/verification.md          live results on PiG 0.5.3
  docs/superpowers/...          this design, the recon reports, the plan
  scripts/live/verify.sh        zero-spend live check against real pig (see 5.7)
  sol-pi.example.json           same keys as SoL-Pi's
  README.md  LICENSE  SECURITY.md  THIRD_PARTY_NOTICES.md  UPSTREAM.md
```

- Extension identity is `sol-pig`: PiG requires `sdk.New(name)` to equal the package member directory name.
- `UPSTREAM.md` records the SoL-Pi commit ported (`2195680`) and the PiG tag the copied files come from (`v0.5.3`), per file. Re-syncing means diffing SoL-Pi from the recorded commit and updating the acceptance list.
- `LICENSE` follows SoL-OpenCode: MIT, `Copyright (c) 2026 SoL-PiG contributors` plus NVIDIA's line for the ported SoL-Pi code. `SECURITY.md` points to the repository's GitHub Security Advisories page, as SoL-OpenCode's does.
- Repository: public, `balcsida/SoL-PiG`, created 2026-10-24 at the owner's request. The builder pushes to `main` as phases complete and enables Actions; tagging `v0.1.0` waits for the owner's go-ahead.
- Install SoL-PiG or SoL-Pi in a PiG, not both: they register the same tools and share the runtime root. The README says so; no runtime guard.

### 5.2 Names kept from SoL-Pi

Unchanged, for compatibility with SoL-Pi documentation, configs and sessions (PiG's shared mode can reuse Pi's directories):

- Config file `sol-pi.json`, same schema, defaults and validation messages (spec §0.2). Search order: `<cwd>/<project config dir>/sol-pi.json` when the project is trusted, else `<agent dir>/sol-pi.json`, else defaults; the project file replaces the global one.
- Tool names `edit`, `write` (with `then_run`), `obs_recall`, `update_plan`, with SoL-Pi's descriptions, schemas and prompt guidelines verbatim (spec §1.2, §2.3, §4.3).
- Runtime root `<session dir>/sol-pi/<session id>` and the `sol-pi-<id>-*` temp fallback; file layouts under it (spec §0.4, §2.6, §3.5).
- Session entry types `sol-pi-online-context-state-v1`, `sol-pi-online-context-compact-skipped`, `sol-pi-evidence-preserving-reducer-v1`, custom message `sol-pi-online-context-compact`; receipt prefix `sol_pi_evidence_receipt_v1`; schema ids; status key `sol-pi-savings`.
- Reminder texts, placeholder text, receipt text, reducer prompts, error messages (spec §4.6, §2.5, §3.7, §3.9).

One deliberate change: the product name in user-facing banners and notifications is `SoL-PiG` (`⚡ SoL-PiG · Action Fusion`, `Money saved · …`). Everything the model sees is unchanged.

### 5.3 Shared infrastructure

- **Lazy registration.** The factory registers only `session_start`. That handler runs once: load config, then register the enabled mechanisms in SoL-Pi's order (Action Fusion, ObservationPack, Reducer, OCC — OCC's `context` handler must run after ObservationPack's). PiG supports both late `OnEvent` and late `RegisterTool` (a live `registerTool` host call that refreshes the session). A config error returns an error from the handler and registers nothing, matching SoL-Pi's "stop loading with a direct error".
- **Directories.** The SDK exposes no agent-dir helper, so `internal/paths` replicates PiG's rules: shared mode iff `PIG_USE_PI_DIRS=1`; agent dir = `PIG_CODING_AGENT_DIR` (default mode) or `PI_CODING_AGENT_DIR` (shared) when set, else `~/.pi/agent` in shared mode, else `<config root>/agent` where config root = `$PIG_HOME`, else `$XDG_CONFIG_HOME/pig`, else `~/.pig`; project config dir `.pig`, or `.pi` in shared mode. Session dir and id come from `ctx.SessionManager()`; the id must match SoL-Pi's safe-id rule.
- **Savings display.** Only when `ctx.Mode() == "tui"`: `ctx.Notify("⚡ SoL-PiG · <Mechanism>\nMoney saved · <saving>", "info")` and `ctx.SetStatus("sol-pi-savings", "⚡ <Mechanism> · <saving>")`, cleared after 4 s. The builder verifies that a retained `Context` may still call `SetStatus` from a timer after its handler returned; if PiG rejects that, clear the status on the next event the extension handles instead, and record the difference in `docs/compatibility.md`.
- **Tool renderers.** Go renderers return lines. `obs_recall` and `update_plan` render SoL-Pi's banner plus base line. The `edit`/`write` overrides define no renderers: PiG then uses its built-in edit/write rendering (diffs intact), and the Action Fusion saving shows through notify/status only. This is a visible difference from SoL-Pi (no banner row on fused edits) and is recorded in `docs/compatibility.md`.
- **Logging.** Fail-open paths log one line to stderr with SoL-Pi's wording (`[observationpack] fail-open for tool result: …`). Nothing to stdout, no `os.Exit`, no `os.Chdir`: PiG rejects these in fusable factories.

### 5.4 Action Fusion

- Override `edit` and `write` by same-name `RegisterTool`. Parameters = PiG's built-in schema for that tool plus the optional `then_run` object `{command: string, timeout?: number}` with SoL-Pi's exact descriptions; property order `path, content|edits, then_run`. Description, label, prompt snippet and guidelines copied from PiG's built-in definitions so the model sees identical text apart from `then_run`.
- Execute = PiG's own edit/write implementation, copied from `internal/codingagent/tools/{edit,edit_diff,edit_preview,diff_string,unified_patch,write}.go` into `internal/pitools` with attribution and the source commit recorded in `UPSTREAM.md`. Copying, not re-porting from Pi, keeps the override byte-identical to what PiG does without SoL-PiG. Result `content`/`details` shapes stay PiG's.
- Per-path serialization: PiG's file-mutation queue is not available to Go extensions, so `internal/actionfusion` serializes mutation plus `then_run` per canonical path with an in-process lock (SoL-Pi's `withFusedFileQueue` semantics, including the canonical-key rules). Ordering against PiG's other built-in tools is not preserved; `docs/compatibility.md` says so.
- `then_run` runs after a successful mutation and SoL-Pi's unchanged-content check (sha256 before and after a yield), through the host: `ctx.ExecWithOptions(shell, ["-c", prefix+command], {Timeout: seconds*1000, Cwd: ctx.Cwd()})`, with `shellPath` and `shellCommandPrefix` taken from `ctx.GetSettings()` (PiG already applies the project-trust rule to settings).
- Policy: because `then_run` does not go through the `bash` tool, `tool_call` hooks that other extensions place on `bash` do not see it. This is SoL-Pi's behaviour too (it calls the bash tool's `execute` directly). Hooks on `edit` and `write` do see `then_run` in the tool input. `SECURITY.md` states this. Output is formatted the way PiG's bash tool formats it: combined output, PiG's truncation limits, a trailing `Command exited with code N` line on non-zero exit, and the full-output log file PiG's bash tool writes when it truncates, with the same name pattern and location, because the reducer's candidate extraction parses exactly these. The builder takes the formatting and truncation code from PiG's bash tool files rather than inventing it. Markers `[then_run:succeeded]`, `[then_run:failed]`, `[then_run:skipped]`, the failure-keeps-mutation rule, the seconds unit for `timeout`, nested-call detection for savings, and `resolveToolPath` (Unicode spaces, leading `@`, `file://`, `~`, Windows drive forms) are ported verbatim (spec §1.3–1.5).

### 5.5 ObservationPack

- `OnEvent("context")`: scan `event["messages"]` for pure-text, non-error tool results above 10,240 UTF-8 bytes; archive content-addressed objects (`obs_` + 24 hex, dir 0700, file 0600, `O_EXCL`, no symlink following where the platform supports it); send in full for the first two provider requests, then replace `content` in place with SoL-Pi's placeholder; ledger JSONL; per-message fail-open with the original untouched. Return nothing so PiG keeps the in-place edits and system-message positions. Request index and send counts as in spec §2.4.
- `obs_recall` tool and paged recall exactly as spec §2.3, including the two-line header, hard limits, and error messages.

### 5.6 Evidence-Preserving Reducer

- `OnEvent("tool_result")` with the decision flow of spec §3.3: skip nested results (`parentToolCallId`), candidate rules (`bash` matching `DIAGNOSTIC_COMMAND`, or `write`/`edit` with a `then_run` marker), minimum 4,096 bytes, exit-state consistency, size and likely-secret guards, archive, model call, receipt verification (exact byte-for-byte quotes, hash, status, limits), receipt must be smaller than the source, journal entries through `ctx.AppendEntry`. The handler returns `{content, details, isError}` only when a receipt is applied; otherwise nothing.
- Body source: inline text, or the full-output log PiG's bash tool names in the result (`Full output:` hint or `details.fullOutputPath`), accepted only when it sits directly in the OS temp dir and is a regular file. PiG writes that log as `pi-bash*.log` in the temp dir and the same `[Showing lines X-Y of Z. Full output: <path>]` row as Pi (PiG `internal/codingagent/tools/bash_executor.go:77`, `shell_tool.go:246-252`), so SoL-Pi's matching rules apply unchanged.
- Model call: `ctx.ModelRegistry().Find(provider, model)`; unavailable → `reducer-model-unavailable` fallback, no call. Request as spec §3.6 (`systemPrompt`, one user message, `cacheRetention: "none"`, `maxTokens: min(2048, model max)`, `sessionId: runId`), 90 s timeout. The Go `Complete` returns an untyped map and takes no signal; the builder checks which timeout/cancellation option the Go SDK honours and otherwise bounds the call with a timer, treating expiry as `model-call-timeout`. Defaults stay `openai-codex` / `gpt-5.6-luna`; credentials stay PiG's.

### 5.7 Online Context Compact

The most host-sensitive mechanism. The plan ports it last and treats PiG as the "deferred host" SoL-Pi already supports (Pi ≥ 0.87 semantics; PiG pins Pi 1.0.3).

- `update_plan` tool: schema, guidelines, `ExecutionMode: "sequential"`, model-only exposure, plan parsing, transitions and advice strings as spec §4.3; state saved with `ctx.AppendEntry("sol-pi-online-context-state-v1", state)` on every call.
- Events and state machine as spec §4.1–4.4 and §4.8: `session_start`/`session_tree` restore from `GetBranch()`; `context` records observed messages and a pending request; `before_provider_request` counts the request; `message_end`, `input` (steer/`CORRECTION:` resets), `turn_end` evaluates the pending boundary and, when the economics select compaction, calls `ctx.Abort()`; `agent_settled` waits for `IsIdle()`, then `ctx.CompactWithOptions{CustomInstructions, OnComplete, OnError}` and blocks in the handler until a callback fires; then sends the reminder with `ctx.SendMessage("sol-pi-online-context-compact", text, false, {TriggerTurn: sdk.Bool(true)})`. PiG starts that turn after every settled handler returns, which is exactly SoL-Pi's deferred-host branch, so the extension does not wait for a later `agent_settled`. `session_before_compact` cancels PiG's own threshold compaction while a selection is pending; `session_compact` records the compaction and debt; `session_before_tree` cancels navigation while compaction is in flight; `session_shutdown` clears in-memory state.
- Recoverable refusals (`Nothing to compact (session too small)`, `Already compacted`, the incomplete-summary message) behave as spec §4.4: skip entry, warning in TUI, skipped-reminder, no retry on plan-only boundaries. PiG uses Pi's wording (`coding/session.go:2381-2383`; `:1922` for `generation hit the token cap and the summary is incomplete`); match these by substring, since PiG may prefix them.
- Economics, plan and state transitions are pure and ported verbatim with their tests (spec §4.7–4.8).
- Projection (`archiveTokens`, spec §4.9) needs Pi's `findCutPoint`, `sessionEntryToContextMessages`, `estimateTokens` and, where PiG's pinned Pi has them, the projection that applies `context_edit` entries. These live under PiG's `internal/`, so `internal/pisupport` holds copies adapted to the `map[string]any` session entries `GetBranch()` returns, with attribution. `estimateTokens` follows PiG's `agent/context_tokens.go` (chars/4 per block, same block rules) rather than importing PiG's root module, which would drag in ~40 dependencies.
- Settings: `keepRecentTokens` and `reserveTokens` from `ctx.GetSettings()` (`compaction.modelOverrides["provider/id"]`, then `compaction.*`, then 20,000 / 16,384); context window from `GetContextUsage()`, else `GetModelInfo().ContextWindow`.

### 5.8 Testing (zero spend)

1. **Unit tests** (`go test ./...`) for all pure logic, one test per acceptance line in spec §7 that does not require Pi's runtime: config, paths, savings formatters, path resolution, then_run markers and queue, observation/placeholder/recall, candidate/archive/receipt/decision flow, economics, plan, state, projection.
2. **Fake host.** `internal/testhost` speaks PiG's subprocess wire over `net.Pipe()`, following `examples/extensions/plan-mode/main_test.go` in PiG: perform the register/ready handshake, deliver events (`session_start` with a temp session dir, `context`, `tool_result`, `turn_end`, `agent_settled`, `session_compact`, …), invoke tools, and answer host calls (`getBranch`, `appendEntry`, `sendMessage`, `compact` with scripted callbacks, `isIdle`, `getContextUsage`, `getSystemPrompt`, `getSettings`, `modelRegistry.find/complete` with scripted replies, `exec`). The mechanism-level tests that SoL-Pi runs against its `FakePi` run here. It stays minimal: it encodes the wire, not PiG's timing; the live check below is what proves the choreography against PiG.
3. **Live check, end to end, zero spend.** `scripts/live/verify.sh` runs real `pig` ≥ 0.5.3 with an isolated `PIG_HOME`, the package installed through settings and all four mechanisms enabled. Two model backends:
   - PiG's built-in fake provider (`PIG_TEST_FAUX=1`) for parity of the overridden tools: `Run: write parity-write-output.txt` and `Run: edit parity-edit-target.txt` produce the same files and tool results with and without SoL-PiG; `Run: bash long output` passes through the hooks with its outcome unchanged.
   - A scripted local OpenAI-compatible endpoint, registered through `models.json` (`"api": "openai-completions"`, `127.0.0.1`, dummy key; PiG documents this in `docs/site/docs/custom-provider.md`), for everything the fake provider cannot drive: `edit`/`write` calls with `then_run`; a tool result large enough to be placeholdered on its third send and recalled with `obs_recall`; a diagnostic `bash` command whose output the endpoint then reduces with a valid receipt quoting the archived log; an `update_plan` sequence that completes steps with enough history that OCC selects compaction, where the endpoint also answers PiG's compaction summary prompt, and the hidden reminder starts the next turn. The endpoint is a small Go program in its own module under `scripts/live/mockprovider/` (outside `extensions/`, so PiG's discovery never sees it), modelled on SoL-OpenCode's `scripts/live/mock-provider.mjs`. The check prints what the endpoint received per request next to the session file, and the idle footprint of pig plus the runner.
   - Before OCC is ported (phase 4), the same scripted endpoint runs SoL-Pi itself (Node) under PiG. If PiG completes SoL-Pi's abort → `agent_settled` → compact-with-callbacks → reminder sequence there, the host behaviour is proven independently of the port; if not, that is a PiG parity bug to report, and OCC waits for it. Recorded in `docs/verification.md`.
   "Done" for every mechanism is: unit tests green, fake-host tests green, and the relevant live scenario passing on PiG 0.5.3. No paid verification phase.
4. **CI** (GitHub Actions): `go vet`, `go test`, the live check with `pig` installed from npm `@pi-in-go/pig@0.5.3` or `go install github.com/MichaelKinsy/PiG/cmd/pig@v0.5.3`; the builder picks whichever is faster and documents it.

### 5.9 Build order

Phases for the plan, each ending with green tests and a live check where applicable:

0. Scaffold: repository, module, factory with lazy registration, config, paths, savings, fake host, CI, validate-only passes against pig 0.5.3.
1. ObservationPack (smallest host surface; proves the `context` hook and the fake host).
2. Action Fusion (copied PiG edit/write, then_run through `ExecWithOptions`).
3. Evidence-Preserving Reducer.
4. Online Context Compact.
5. Docs, `docs/verification.md` with measured footprint, release tag `v0.1.0`.

All four mechanisms are in scope for `v0.1.0`, OCC last. If OCC is held up by a PiG parity bug (5.8), `v0.1.0` ships with the other three and OCC follows in `v0.2.0`.

### 5.10 Sync with SoL-Pi

SoL-Pi is active (six commits on 2026-10-17/18 alone). `UPSTREAM.md` records the ported commit; each Go file ported from SoL-Pi names its source file in a header comment. A sync is: diff SoL-Pi since the recorded commit, update the behaviour spec and acceptance list, port, bump the recorded commit. No automation in v0.1.

### 5.11 PiG upstream issues (parallel track)

Four of the workarounds above exist because PiG's Go SDK lacks something Pi gives TypeScript extensions. They are worth issues in PiG, which treats such gaps as parity work; the port does not wait for them, and when PiG ships one, the copy or workaround is replaced:

1. File-mutation queue for tool overrides in the Go, Rust and Python SDKs (PiG's own matrix marks it partial). Replaces the in-process per-path lock in 5.4.
2. A way for a tool override to delegate to, or at least fetch the definition of, the built-in tool it replaces. Replaces the copied edit/write code in 5.4.
3. Agent-directory and project-config-directory helpers in the Go SDK, matching what the Node runtime bridges for `getAgentDir` and `CONFIG_DIR_NAME`. Replaces the path rules in 5.3.
4. Public Go packages for the compaction helpers Pi serves to extensions (`findCutPoint`, `sessionEntryToContextMessages`, `estimateTokens`). Replaces the copies in 5.7.

The owner posts them, from drafts kept in `docs/superpowers/pig-issues.md` until they are filed.

## 6. Left to the builder

- Internal package boundaries and names beyond those above; wire-level details of the fake host.
- Exactly which PiG files to copy for edit/write and for the bash output formatting, and how to keep the copies minimal; the attribution header text.
- Platform specifics: `O_NOFOLLOW` where available, Windows best effort.
- The timer-based status clearing versus clearing on the next event (5.3) and the model-call timeout mechanism (5.6), each settled by checking PiG's source and recorded in `docs/compatibility.md`.
- CI provisioning of `pig`.
- README structure; SoL-OpenCode's README, SECURITY.md and THIRD_PARTY_NOTICES.md are the models to follow.

## 7. Owner's answers

1. Scope: the owner did not single out mechanisms, so all four are in `v0.1.0`, OCC last (5.9). The owner asked for PiG issues on the missing capabilities instead; these run in parallel (5.11).
2. Repository: public, created 2026-10-24 at the owner's request (5.1). Tagging `v0.1.0` still waits for the owner's go-ahead.
