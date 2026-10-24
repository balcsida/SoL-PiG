# PiG issues to file (drafts)

Four gaps in PiG's Go extension SDK that SoL-PiG works around (design §5.11). Each is a Pi-parity gap: Pi gives TypeScript extensions the capability, PiG's Go/Rust/Python SDKs do not. File them in MichaelKinsy/PiG; record the issue number here once filed. SoL-PiG does not wait for them.

## 1. File-mutation queue for tool overrides in the Go, Rust and Python SDKs

Filed: https://github.com/MichaelKinsy/PiG/issues/318

**Title:** Go/Rust/Python SDKs: no file-mutation queue for extension tools that override `edit`/`write`

**Body:**

`docs/extension-api-parity.md` marks the file-mutation queue row partial: the host's built-in tools serialize through the queue and the Node runtime's `withFileMutationQueue` queues inside the extension process, but Go, Rust and Python have no equivalent.

An extension that overrides `edit` or `write` by same-name registration (which works, per `TestBuiltInOverride*`) therefore loses per-path serialization against the host's other tools, such as `bash`, and must serialize its own calls itself. Pi gives a TypeScript override `withFileMutationQueue`.

Ask: either serialize an overriding tool's execution through the host's queue when its name is `edit` or `write` (or when the definition opts in), or expose a queue API in the three SDKs, e.g. `ctx.WithFileMutationQueue(path, func() error)`.

Context: porting SoL-Pi's Action Fusion (fused `edit`/`write` + `then_run`) to a Go extension.

## 2. Let a tool override delegate to, or read, the built-in definition it replaces

Filed: https://github.com/MichaelKinsy/PiG/issues/319

**Title:** SDKs: a tool that overrides a built-in cannot delegate to it or read its definition

**Body:**

Pi's TypeScript extensions extend a built-in tool by importing `createEditToolDefinition` / `createWriteToolDefinition` / `createBashToolDefinition` from `pi-coding-agent`, spreading the definition and adding parameters, then calling the original `execute`. PiG's Node runtime serves those functions (`docs/extension-sdk-surface.md`), but the Go, Rust and Python SDKs have nothing equivalent, and PiG's Go implementations live under `internal/`.

A Go extension that wants `edit` with one extra optional parameter has to copy PiG's edit implementation and keep it in sync.

Ask: an SDK call that returns the built-in definition (name, description, prompt snippet and guidelines, parameters schema, execution mode) and a way to invoke the built-in's execute with the same arguments and context, so an override can wrap it. Even the definition alone would remove the schema and prompt-text copying.

Context: same port as above.

## 3. Agent directory and project config directory helpers in the Go SDK

Filed: https://github.com/MichaelKinsy/PiG/issues/320

**Withdrawn 2026-10-24:** SDK v0.5.3 already provides `sdk.AgentDir()` and `sdk.ConfigDirName()` through `PIG_EXT_HOST_INFO` (`extensions/sdk/host_info.go`). Closed as not planned.

**Title:** Go SDK: no equivalent of `getAgentDir()` / `CONFIG_DIR_NAME`

**Body:**

The Node runtime bridges `getAgentDir` and `CONFIG_DIR_NAME` from `pi-coding-agent` to PiG's directories (`docs/extension-sdk-surface.md` lists both as bridged). The Go SDK exposes only `Context.ConfigHome()`, which returns `$PIG_HOME` or `~/.pig`, ignores `XDG_CONFIG_HOME`, and is the config root rather than the agent directory.

A Go extension that reads its own config file from the agent directory or from `<cwd>/.pig` has to re-implement PiG's rules: `PIG_USE_PI_DIRS`, `PIG_CODING_AGENT_DIR` / `PI_CODING_AGENT_DIR`, `PIG_HOME`, `XDG_CONFIG_HOME`, `~/.pig/agent`, `.pig` vs `.pi`.

Ask: `Context.AgentDir() string` and `Context.ProjectConfigDirName() string` (or a single `Context.Paths()`), with the same answers the host uses, in all three SDKs.

## 4. Public Go packages for the compaction helpers Pi serves to extensions

Filed: https://github.com/MichaelKinsy/PiG/issues/321

**Title:** Go SDK: `findCutPoint`, `sessionEntryToContextMessages` and `estimateTokens` are not importable

**Body:**

Pi exports `estimateTokens`, `findCutPoint` and `sessionEntryToContextMessages` from `pi-coding-agent`, and PiG's Node runtime serves them as Pi's own code. PiG's Go ports are `internal/codingagent/compaction.FindCutPoint`, `internal/codingagent.SessionEntryToContextMessages` and `internal/codingagent/compaction.EstimateTokens`; only `agent.EstimateTokens` is public, and importing it pulls in PiG's whole root module.

A Go extension that prices what PiG's native compaction would remove (as SoL-Pi's Online Context Compact does) has to copy these.

Ask: a small public package (for example under `extensions/sdk/compaction`) with these three functions over the session-entry shape that `SessionManager.GetBranch()` returns, kept in step with the host's own implementation.
