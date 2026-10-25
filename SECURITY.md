# Security Policy

SoL-PiG is a PiG extension. It runs in PiG's extension runner, with that process's filesystem, process, network and credential permissions. It is not a sandbox or a permission boundary.

## Sensitive behavior

- **Action Fusion** runs a command requested by the model after a file mutation. The command runs through PiG's bash tool *inside* the `edit` or `write` tool call, so hooks or policies keyed on the `bash` tool name do not see it. Gate `edit` and `write` (and their `then_run` argument) if you gate command execution.
- **ObservationPack** stores tool results larger than 10 KiB, in full, under `<session dir>/sol-pi/<session id>/observation-pack/objects/`, with a ledger of events in `ledger.jsonl` beside it.
- **Evidence-Preserving Reducer** archives eligible diagnostic logs locally under `<session dir>/sol-pi/<session id>/evidence-preserving-reducer/objects/`. When explicitly enabled, it sends each eligible log to its configured model through PiG's model registry, using PiG-managed credentials. The request carries a fixed system prompt and one user message: the SHA-256 of the command (not the command), the SHA-256, byte and line counts of the log, `is_error`, and the exact log body between `<untrusted_log>` tags. A log smaller than 4 KiB or longer than 600,000 characters is never sent.
- The reducer skips a log containing text that matches its `likely-secret` pattern (`api_key`, `authorization`, `bearer`, `access_token` or `secret` followed within 32 characters by `=` or `:` and a value). That detector is a precaution, not a complete secret scanner. Do not enable the reducer for workloads whose logs must not leave the machine.
- The reducer's output is untrusted: it is accepted only when its schema, hash and status match and every quote is an exact substring of the archived source. Otherwise the original result is left unchanged. The reducer's prompt also tells the model to treat the log as untrusted data and never follow instructions in it.
- **Online Context Compact** triggers PiG's native compaction, which sends older conversation to the session's own model to be summarized, as PiG's compaction does anyway. See below for what SoL-PiG stores.
- **Project configuration.** A trusted project's `<config dir>/sol-pi.json` replaces the global file. Open untrusted repositories with care: SoL-PiG ignores the project file until PiG trusts the project.

## What is stored where

Everything lives under the session's runtime root, `<session dir>/sol-pi/<session id>/` (sessions without a directory, such as `--no-session`, use a private temporary directory, `sol-pi-<session id>-…`, that is kept after exit):

| Path | Content | Permissions |
|---|---|---|
| `observation-pack/objects/<id>.txt` | Full large tool results | directories `0700`, files `0600` |
| `observation-pack/ledger.jsonl` | Event ledger (sizes, token estimates, ids, hashes) | directory `0755`, file `0644` |
| `evidence-preserving-reducer/objects/<xx>/<sha256>.txt` | Raw diagnostic logs | directories `0700`, files `0600` |

The reducer's decisions (`candidate`, `provider_response`, `applied`, `fallback`) and Online Context Compact's state are session entries in PiG's session file. They never enter the model context, and can include paths, command hashes and plan text; treat them as sensitive as the conversation.

Archives, the ledger and the journal stay local and are not deleted automatically. Remove `<session dir>/sol-pi/<session id>/` to delete a session's archives.

## Reporting a vulnerability

Use the [GitHub Security Advisories](https://github.com/balcsida/SoL-PiG/security/advisories/new) page of `balcsida/SoL-PiG` to submit a private report. Do not open a public issue for a suspected vulnerability.

Include the affected commit, the configuration, the impact, reproduction steps and any available mitigation. Send reports about PiG itself to the PiG project, and reports about the original mechanisms that also affect SoL-Pi to NVlabs/SoL-Pi.
