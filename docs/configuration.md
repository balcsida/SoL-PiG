# Configuration

SoL-PiG reads one effective configuration per PiG process, at its first `session_start` (guarded by a `sync.Once`). A later session in the same process keeps the first session's project directory and trust decision; restart PiG to pick up a change. Every mechanism is disabled unless the configuration enables it.

## Sources

The first source that exists wins. Sources are never merged.

1. `<project>/<config dir>/sol-pi.json`, read only when PiG reports the project as trusted. `<project>` is the session's working directory. `<config dir>` is the name PiG hands the extension: `.pig` (`.pi` when PiG shares Pi's tree).
2. `<agent dir>/sol-pi.json`, where `<agent dir>` is the agent directory PiG hands the extension, normally `~/.pig/agent` (as reported by the PiG host through the SDK's `AgentDir()`).
3. Built-in defaults, with everything disabled.

SoL-PiG takes both directories from the facts the PiG host passes to every extension. Run outside a PiG host, the extension fails to start with `sdk: the PiG host did not provide its configuration facts (PIG_EXT_HOST_INFO)`. A configuration error is fatal too: it stops loading with the message below and is not retried in the same process.

SoL-PiG reads no dedicated environment variables.

## Schema

```json
{
  "version": 1,
  "actionFusion": false,
  "observationPack": false,
  "evidencePreservingReducer": false,
  "evidencePreservingReducerProvider": "openai-codex",
  "evidencePreservingReducerModel": "gpt-5.6-luna",
  "onlineContextCompact": false,
  "cacheWriteReadRatio": 12.5
}
```

The schema, defaults and messages are SoL-Pi's. `<file>` below is the path of the offending file.

| Key | Type | Default | Validation message |
|---|---|---|---|
| `version` | `1` | required | `SoL-Pi config version must be 1: <file>` |
| `actionFusion` | boolean | `false` | `SoL-Pi config actionFusion must be boolean: <file>` |
| `observationPack` | boolean | `false` | `SoL-Pi config observationPack must be boolean: <file>` |
| `evidencePreservingReducer` | boolean | `false` | `SoL-Pi config evidencePreservingReducer must be boolean: <file>` |
| `onlineContextCompact` | boolean | `false` | `SoL-Pi config onlineContextCompact must be boolean: <file>` |
| `evidencePreservingReducerProvider` | non-empty string | `openai-codex` | `SoL-Pi config evidencePreservingReducerProvider must be a non-empty string: <file>` |
| `evidencePreservingReducerModel` | non-empty string | `gpt-5.6-luna` | `SoL-Pi config evidencePreservingReducerModel must be a non-empty string: <file>` |
| `cacheWriteReadRatio` | finite number ≥ 0 | `12.5` | `SoL-Pi config cacheWriteReadRatio must be a finite non-negative number: <file>` |

Other errors:

- Malformed JSON, or an unreadable file: `Unable to read SoL-Pi config <file>: <reason>`.
- A JSON document that is not an object: `SoL-Pi config must be a JSON object: <file>`.
- A key outside the table: `Unknown SoL-Pi config key: <key>`.

Feature keys may be omitted. Surrounding whitespace is removed from the reducer's provider and model. `cacheWriteReadRatio` prices the extra cost of a cache write as `max(0, ratio − 1)` reads, so a value of 1 or below adds no write cost.

## Feature behavior

- **`actionFusion`:** adds an optional `then_run: { command, timeout? }` (timeout in seconds) to the `edit` and `write` tools. After a successful mutation, the command runs through PiG's bash tool, using the shell and command prefix from PiG's own settings. A non-zero exit is reported but keeps the edit.
- **`observationPack`:** replaces successful, text-only tool results (joined text blocks over 10,240 bytes; results carrying the reducer's receipt prefix are skipped) in outgoing requests after their second full send, and registers `obs_recall`. The stored history is not edited.
- **`evidencePreservingReducer`:** reduces long output of diagnostic commands (bash, or the `then_run` command of `edit`/`write`) through the configured model. Logs under 4 KiB, over 600,000 characters, or matching the secret guard are left alone, as is every result the model's receipt fails to verify.
- **`onlineContextCompact`:** registers `update_plan` and compacts at completed plan steps when the economic check or window pressure selects it (window pressure forces it when tokens would be saved; a selected boundary stops the active run and resumes it with a hidden reminder).

## Runtime inputs

- **Reducer route.** `evidencePreservingReducerProvider` and `evidencePreservingReducerModel` name a model in PiG's model registry (for example one you added in `models.json`). PiG resolves the model and its credentials; never put credentials in `sol-pi.json`. If the registry has no such model, the reducer leaves the result unchanged and records a `reducer-model-unavailable` fallback in its journal. The default route (`openai-codex`/`gpt-5.6-luna`) works only if PiG has that model and your login for it. Otherwise each eligible log is archived locally, the tool output passes through unchanged and the journal records a fallback (`reducer-model-unavailable`, `model-response-error`, `model-call-exception` or `model-call-timeout`). Point `evidencePreservingReducerProvider`/`evidencePreservingReducerModel` at a model you have. A model call is abandoned after 90 seconds, with the result left unchanged.
- **`cacheWriteReadRatio`.** The single ratio Online Context Compact's economic check uses to price the cache rewrite after a compaction. It is fixed for the PiG process, reads no model prices and is not a cost report.
- **PiG's `compaction.*` settings.** Online Context Compact reads `compaction.keepRecentTokens` (default `20000`) and `compaction.reserveTokens` (default `16384`) from PiG's settings, preferring the entry in `compaction.modelOverrides["<provider>/<model id>"]` for the active model. They decide how much recent context survives a compaction and how much of the context window is kept free. The compaction itself is PiG's native one.
- **Context window and usage.** Read from PiG for the active model.
- **Storage.** Archives and ledgers live under `<session dir>/sol-pi/<session id>/`; see [SECURITY.md](../SECURITY.md#what-is-stored-where).

## Live checks

`scripts/live/verify.sh` and `scripts/live/sol-pi-under-pig.sh` set PiG's `compaction.enabled` to `false` for the Online Context Compact scenario, so that PiG's own threshold compaction cannot fire first and fake a pass at the small test window (60,000 tokens). In normal use leave it on: PiG's threshold compaction and Online Context Compact run side by side.
