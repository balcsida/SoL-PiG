# SoL-PiG

SoL-PiG brings [SoL-Pi](https://github.com/NVlabs/SoL-Pi)'s four token-efficiency mechanisms to [PiG](https://github.com/MichaelKinsy/PiG) as a native Go extension. It is a port of SoL-Pi, which NVIDIA released under the MIT license for the Pi coding agent. SoL-PiG is not an NVIDIA or PiG project.

**Spend less without making the agent do less useful work.** The four mechanisms are **Action Fusion**, **ObservationPack**, the **Evidence-Preserving Reducer** and **Online Context Compact**. Each is opt-in and disabled by default. The texts the model sees (tool names, descriptions, placeholders, receipts, reminders) are byte-identical to SoL-Pi at commit `2195680`; only the product name shown to you is SoL-PiG.

## Why Go

SoL-Pi is a TypeScript extension, so every PiG instance that loads it also starts a Node process. SoL-PiG runs in PiG's Go extension runner instead. Idle footprint of 10 instances, measured on one machine:

| Setup | 10 instances |
|---|---|
| bare PiG | 116 MiB |
| PiG + a minimal Go extension | 202 MiB |
| PiG + SoL-Pi (Node) | 1,407 MiB |
| PiG + SoL-PiG | about 30 MiB per instance (two processes: pig and the extension runner) |

Step 3 of `scripts/live/verify.sh` measures one instance on your machine.

## Requirements

- PiG **0.5.3** or newer
- Go **1.26** or newer, for the first build: PiG compiles the extension on install. A cold first install took about 3 minutes on a loaded machine; later installs are cached.

## Install

```bash
pig install git:github.com/balcsida/SoL-PiG
```

From a local checkout:

```bash
git clone https://github.com/balcsida/SoL-PiG
pig install /path/to/SoL-PiG
```

Install SoL-PiG or SoL-Pi in a PiG, not both: they register the same tools and share the runtime directory.

## Configure

This conservative configuration enables only the two local mechanisms that make no extra model calls and do not stop an active run:

```json
{
  "version": 1,
  "actionFusion": true,
  "observationPack": true,
  "evidencePreservingReducer": false,
  "onlineContextCompact": false,
  "cacheWriteReadRatio": 12.5
}
```

SoL-PiG reads `sol-pi.json` from the directories PiG reports to the extension, in this order:

1. `<project>/<config dir>/sol-pi.json`, when the project is trusted and the file exists. `<project>` is the session's working directory; `<config dir>` is `.pig` (`.pi` when PiG shares Pi's tree).
2. `<agent dir>/sol-pi.json`, where the agent directory is PiG's own, normally `~/.pig/agent`.

With neither file, every mechanism stays off. The project file replaces the global one; they are never merged. An invalid file stops the extension from loading, with a direct error. [sol-pi.example.json](sol-pi.example.json) lists every key; [docs/configuration.md](docs/configuration.md) has the schema, defaults and validation messages.

- `version`: must be `1`.
- `actionFusion`, `observationPack`, `evidencePreservingReducer`, `onlineContextCompact`: booleans, default `false`.
- `evidencePreservingReducerProvider`, `evidencePreservingReducerModel`: the PiG model the reducer calls, default `openai-codex` and `gpt-5.6-luna`; credentials stay in PiG.
- `cacheWriteReadRatio`: how much a cache write costs relative to a read in Online Context Compact's economic check, default `12.5`.

## Mechanisms

| Mechanism | What changes for the model and you | Key |
|---|---|---|
| **Action Fusion** | `edit` and `write` accept an optional `then_run` command that runs after the mutation, in the same tool call, through PiG's bash tool. | `actionFusion` |
| **ObservationPack** | Tool results over 10 KiB are sent in full twice, then replaced in later requests by a short placeholder; the model reads exact slices back with the `obs_recall` tool. | `observationPack` |
| **Evidence-Preserving Reducer** | Long output of diagnostic commands (`make`, `go test`, `cargo`, `pytest`, `lean` and similar) becomes a compact receipt written by a reducer model, accepted only when every quote matches the archived log byte for byte. | `evidencePreservingReducer` |
| **Online Context Compact** | Adds an `update_plan` tool. Completing a plan step becomes a point where PiG's native compaction runs, if an economic check or window pressure says it pays off. | `onlineContextCompact` |

## Seeing it work

In the TUI, each saving shows a notification and, for four seconds, a status line, worded as in SoL-Pi:

```text
⚡ SoL-PiG · Observation Pack
Money saved · 2,531 context tokens avoided
```

The saving text is `1 model round-trip avoided` (Action Fusion), `N context tokens avoided` (ObservationPack), `N KiB removed from future prompts` (the reducer, labelled "Luna Delegating") and `N context tokens removed` (Online Context Compact). The renderers of `obs_recall` and `update_plan` carry the same two-line banner. A compaction that was skipped shows `Online compaction skipped: …` as a warning. Other modes (print, JSON, RPC) stay silent.

`scripts/live/verify.sh` checks all four mechanisms inside a real PiG process: seven steps with PiG's fake provider and a scripted loopback endpoint, zero spend, no credentials. See [docs/verification.md](docs/verification.md).

## Storage and security

ObservationPack and the reducer archive source material under the session's runtime root, `<session dir>/sol-pi/<session id>/`:

```text
observation-pack/              objects/<id>.txt, ledger.jsonl
evidence-preserving-reducer/   objects/<xx>/<sha256>.txt
```

A session without a directory (`--no-session`) gets a private temporary directory instead, `sol-pi-<session id>-…` under the system temp directory. Archives stay local and are not deleted automatically. The reducer may send eligible logs to its configured model. Read [SECURITY.md](SECURITY.md) before enabling it.

## Documentation

| Document | Purpose |
|---|---|
| [docs/configuration.md](docs/configuration.md) | Sources, schema, defaults, runtime inputs |
| [docs/compatibility.md](docs/compatibility.md) | PiG APIs used and every difference from SoL-Pi |
| [docs/verification.md](docs/verification.md) | Live results on PiG 0.5.3 |
| [SECURITY.md](SECURITY.md) | Local storage, remote reduction, `then_run`, reporting |
| [CHANGELOG.md](CHANGELOG.md) | Release notes |
| [UPSTREAM.md](UPSTREAM.md) | Which file ports which SoL-Pi or PiG source, at which commit |

## Development

```bash
cd extensions/sol-pig && go vet ./... && go test ./...   # unit and fake-host tests, zero spend
scripts/live/verify.sh                                    # real pig: fake provider + scripted endpoint, zero spend
scripts/live/sol-pi-under-pig.sh                          # pre-check: SoL-Pi itself under PiG (needs Node >= 22.19, npm, network)
pig install "$PWD" --validate-only --json                 # does PiG accept the package?
```

The live scripts need `pig` and `go` on `PATH`; they write under `scripts/live/work/` (git-ignored) unless you pass a work directory. `.github/workflows/ci.yml` runs the Go checks, the scripted endpoint's tests and `scripts/live/verify.sh` against PiG 0.5.3 on every pull request and push to `main`.

## License

MIT. SoL-PiG derives from SoL-Pi (Copyright © 2026 NVIDIA Corporation & Affiliates, MIT) and copies one normaliser from PiG (Michael Kinsy, MIT); see [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). The research behind the mechanisms is described in the [SoL-Pi paper](https://arxiv.org/abs/2609.20519) and [blog](https://nvlabs.github.io/SoL-Pi/).
