# Compatibility

SoL-PiG ports [SoL-Pi](https://github.com/NVlabs/SoL-Pi) at commit `2195680` to PiG's Go extension SDK. The model-facing text is SoL-Pi's. This page lists what the extension needs from PiG and every place where behaviour differs from SoL-Pi.

## Versions

| | |
|---|---|
| PiG | 0.5.3 (`pig --version` reports `0.5.3+1.4.0.74f46864`) |
| SDK | `github.com/MichaelKinsy/PiG/extensions/sdk` v0.5.3 |
| Go | `go 1.26` (the `go` directive in `extensions/sol-pig/go.mod`); PiG builds the extension with the installed Go toolchain on first load |

## SDK surfaces used per mechanism

Every identifier below was checked against the SDK v0.5.3 sources and against the extension's non-test code.

| Mechanism | SDK calls and types | Replaces in SoL-Pi's Pi API |
|---|---|---|
| Loader (`extension.go`) | `sdk.New`, `Extension.OnSessionStart`, `sdk.AgentDir`, `sdk.ConfigDirName`, `sdk.ErrHostInfoUnavailable`, `Context.IsProjectTrusted`, `Context.Cwd` | `getAgentDir()`, `CONFIG_DIR_NAME`, the factory's own registration |
| Action Fusion | `sdk.CreateEditToolDefinition`, `sdk.CreateWriteToolDefinition`, `sdk.CreateBashToolDefinition`, `sdk.BashToolOptions`, `sdk.BuiltinToolDefinition` (`ExecuteWith`), `sdk.ToolDefinition` (`PrepareArguments`, `Execute`), `Extension.RegisterTool`, `sdk.SettingsManagerCreate` / `SettingsManagerCreateOptions`, `sdk.AgentToolResult`, `sdk.ToolResultContent`, `Context.Signal`, `Context.ToolCallID`, `Context.Notify` | `createEditToolDefinition`, `createWriteToolDefinition`, `createBashToolDefinition`, `SettingsManager.create`, `pi.registerTool` |
| ObservationPack | `Extension.RegisterTool` (`sdk.ToolDefinition`, `sdk.Schema`, `sdk.ToolRenderShellSelf`, `sdk.ToolRenderContext`, `sdk.ToolRenderResult`, `sdk.ToolRenderResultOptions`, `sdk.ToolResult`), `Extension.OnEvent(sdk.EventContext, …)`, `Context.SessionManager` (`GetSessionDir`, `GetSessionID`) | `pi.registerTool` with `renderShell: "self"`, `pi.on("context")`, `ctx.sessionManager` |
| Evidence-Preserving Reducer | `Extension.OnEvent(sdk.EventToolResult, …)` (the returned patch replaces the result), `Context.ModelRegistry` (`Find`, `Complete`), `Context.Signal`, `Context.Done`, `Context.AppendEntry`, `Context.SessionManager` | `pi.on("tool_result")`, `ctx.modelRegistry` with `completeSimple`, `pi.appendEntry` |
| Online Context Compact | `Extension.OnEvent` for `sdk.EventSessionStart`, `EventSessionBeforeTree`, `EventSessionTree`, `EventContext`, `EventBeforeProviderRequest`, `EventMessageEnd`, `EventInput`, `EventTurnEnd`, `EventAgentSettled`, `EventSessionBeforeCompact`, `EventSessionCompact`, `EventSessionShutdown`; `Extension.RegisterTool` with `sdk.ToolExposureModelOnly`; `Context.CompactWithOptions` (`sdk.CompactOptions`), `Context.SendMessage` (`sdk.SendMessageOptions`), `Context.Abort`, `Context.IsIdle`, `Context.GetContextUsage`, `Context.GetSystemPrompt`, `Context.GetSettings`, `Context.GetModelInfo` (`sdk.ModelInfo`), `Context.SessionManager` (`GetBranch`, `BuildSessionProjection`, `sdk.ProjectedSessionEntry`), `Context.AppendEntry`, `Context.Mode`, `Context.Notify`, `sdk.FindCutPoint`, `sdk.EstimateTokens` | `pi.on(...)` for the same events, `ctx.compact`, `pi.sendMessage`, `ctx.abort`, `ctx.isIdle`, `ctx.getContextUsage`, `ctx.getSystemPrompt`, `findCutPoint`, `estimateTokens` from Pi's compaction module |
| Savings display (`internal/savings`) | `Context.Mode`, `Context.Notify`, `Context.SetStatus`, `Context.ClearStatus` | `ctx.ui.notify`, `ctx.ui.setStatus` |

## Host behaviours relied on

- **Late registration.** The extension registers only `session_start`; the enabled mechanisms are registered from that handler, once. PiG accepts tools and handlers registered at that point, as Pi does, and a configuration error returned from it stops loading with SoL-Pi's message.
- **In-place `context` edits.** ObservationPack and OCC edit the outgoing messages inside their `context` handlers. PiG applies each handler's edits in registration order, so OCC's handler (registered last) sees ObservationPack's placeholders. Stored history is not changed.
- **`tool_result` replacement.** A patch returned from a `tool_result` handler replaces the content of the result the model receives; returning nothing leaves it alone. The reducer returns a patch only for a verified receipt that is smaller than the log.
- **`agent_settled` and `triggerTurn`.** OCC calls `SendMessage` with `TriggerTurn` from its `agent_settled` handler. PiG starts that turn only after every `agent_settled` handler has returned.
- **`compact` ordering.** PiG delivers `session_compact` before the `compact` call's result, so OCC's `session_compact` handler has already counted the compaction when `CompactWithOptions` completes.

## Differences from SoL-Pi

1. **Edit and write rendering.** The built-in `edit` and `write` are wrapped through `CreateEditToolDefinition` and `CreateWriteToolDefinition`, and PiG draws their call and result. There is no SoL-Pi banner row on fused edits. The saving appears as a notification and status line in the TUI only.
2. **Schema property order.** Tool parameter schemas serialize with properties in byte order (`content, path, then_run`; `edits, path, then_run`; `id, offset`; `progress, steps`) because the SDK encodes Go maps that way. SoL-Pi's order is insertion order. The text is identical.
3. **`then_run` and hooks.** `then_run` runs through PiG's bash built-in via the SDK, so `tool_call` hooks other extensions place on `bash` do not see it, as in SoL-Pi. Hooks on `edit` and `write` see `then_run` in the input. A `then_run` that is not an object is a tool error (`then_run must be an object with a command`) instead of a bash rejection.
4. **File queue scope.** The per-file queue serializes SoL-PiG's own fused calls. Ordering against PiG's other built-in tools on the same file is left to PiG's own queue, under which the mutation itself runs. [PiG #318](https://github.com/MichaelKinsy/PiG/issues/318) asks for a shared file-mutation queue.
5. **Reducer.** An archive integrity failure is journaled as `fallback` with reason `archive-error` instead of throwing. The fork-shaped `getApiKeyAndHeaders` path does not exist. The 90 s bound is the extension's own timer plus the run signal, because the model call has no cancel API. `structuredContent.output` is not used.
6. **OCC.** Only SoL-Pi's "deferred host" branch exists, because PiG starts the triggered turn after all `agent_settled` handlers return. Recoverable compaction errors are matched by substring. Pricing uses the SDK's `FindCutPoint` and `EstimateTokens` over `BuildSessionProjection()`.
7. **Directories.** The agent directory and project config directory name come from PiG's host facts (`PIG_EXT_HOST_INFO`). PiG's defaults are `~/.pig/agent` and `.pig`; with `PIG_USE_PI_DIRS=1` they are Pi's.
8. **Ledger.** ObservationPack ledger JSON keys are in byte order (same cause as item 2). The ledger file is 0644 in a 0700 directory, as SoL-Pi's defaults.
9. **No Node preflight.** SoL-Pi's Node preflight script is not ported. `pig install <dir> --validate-only --json` and the configuration tests cover it.
10. **Temp directory.** The bash full-output log is looked up under a temp directory that follows Node's `os.tmpdir()` rules (`TMPDIR`, `TMP`, `TEMP`, `/tmp`).
11. **Edit argument normaliser.** The `edit` override carries a port of PiG's `prepareEditArguments` (legacy `{path, oldText, newText}`, a single edit object, `edits` as a JSON string) as its `PrepareArguments`, because the SDK's `BuiltinToolDefinition` does not carry the built-in's normaliser. [PiG #322](https://github.com/MichaelKinsy/PiG/issues/322) asks for it. The port is `internal/actionfusion/prepare.go`, PiG-derived and attributed in [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md) and [UPSTREAM.md](../UPSTREAM.md).
12. **Same-batch ordering.** Two fused calls on the same file in one parallel batch may run out of source order: PiG's built-ins reserve their mutation order before resolving the path, while fused calls reach SoL-PiG's queue after it. SoL-Pi has the same gap; it differs only from Action Fusion turned off.
13. **Queue-key fallback.** When the canonical queue key cannot be computed for a reason other than a missing path segment, the fused call queues on the absolute path instead of failing (SoL-Pi throws). The host's own resolution then reports the error.
14. **RE2 regex semantics.** The reducer's regexes are byte-identical to SoL-Pi's but run under Go RE2. `\s` is ASCII `[\t\n\f\r ]` (JS also matches `\v`, U+00A0, U+FEFF and the Unicode spaces), `(?i)` also folds U+212A and U+017F, and `{0,32}` counts code points rather than UTF-16 units. A trailer followed by such a character leaves the exit state unknown instead of matching.
15. **Journal key order.** Reducer journal entries are written through the SDK's JSON encoder, which sorts map keys, so on disk the keys are alphabetical rather than SoL-Pi's `schema, runId, kind, …`. The JSON is equivalent.
16. **Invalid UTF-8.** Invalid UTF-8 in a log is sanitised with Go's `strings.ToValidUTF8`, which replaces each run of invalid bytes with one U+FFFD, where Node emits one per maximal invalid subpart. Hashes, archive, quotes and readback all use the same sanitised body, so nothing a user sees differs.
17. **Compaction in the live checks.** `verify.sh` step 7 and `sol-pi-under-pig.sh` turn PiG's native threshold auto-compaction off (`{"compaction":{"enabled":false}}` in the isolated agent directory's `settings.json`) so the OCC boundary is what compacts at a 60000-token window. In normal use both stay on, as with SoL-Pi under Pi: whichever threshold is reached first compacts, and OCC's `session_before_compact` handler cancels a native threshold compaction only while a boundary is selected.
18. **JSON escaping.** Plan snapshots and plan advice are rendered with Go's JSON encoder with HTML escaping off, so `&`, `<` and `>` appear literally, as with `JSON.stringify`. Go still escapes U+2028 and U+2029 as `\u2028` and `\u2029`, which `JSON.stringify` leaves literal.

## Upstream issues

- [PiG #318](https://github.com/MichaelKinsy/PiG/issues/318): file-mutation queue (item 4).
- [PiG #322](https://github.com/MichaelKinsy/PiG/issues/322): `PrepareArguments` on `BuiltinToolDefinition` (item 11).
- PiG #319, #320 and #321 were withdrawn: the SDK already provided the surface they asked for.
