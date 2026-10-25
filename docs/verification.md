# Verification

SoL-PiG is checked inside a real PiG process with no credentials and no spend. Everything runs in a throwaway `HOME` against PiG's built-in fake provider or a scripted loopback endpoint (`scripts/live/mockprovider`).

## Running the live check

```sh
scripts/live/verify.sh [work-directory]
```

It needs `pig` and `go` on `PATH`. The first run compiles the extension: a cold `pig install --validate-only` compiled the module in about 3 minutes on a loaded machine, and the build is cached afterwards. The script prints one numbered line per step and `verify.sh: ok` at the end; any failure prints `verify.sh: FAIL: …` and exits non-zero.

| Step | What it proves |
|---|---|
| 1 | `pig install <repo> --validate-only --json` builds the module and registers the extension as `sol-pig`. |
| 2 | A session on PiG's fake provider answers, and `obs_recall` and its prompt snippet reach the request. |
| 3 | Measures the idle footprint of one instance: `pig` plus the extension runner. |
| 4 | ObservationPack: a large result is sent in full twice, replaced by a placeholder on the third send, and `obs_recall` returns the archived bytes. |
| 5 | Action Fusion: `then_run` runs inside `write` and `edit`, and the built-in tools produce identical files with the flag off and on. |
| 6 | Reducer: a failing `make` target becomes a verified receipt through the scripted model, with the full log archived. |
| 7 | OCC: a completed plan step aborts, compacts natively and resumes the task. |

## Pre-check: SoL-Pi's own TypeScript under PiG

```sh
SOL_PI_DIR=<SoL-Pi checkout with node_modules> scripts/live/sol-pi-under-pig.sh
```

It needs Node ≥ 22.19. It runs SoL-Pi's own compaction sequence (abort, settle, native compaction, resume) under PiG, which is the behaviour the OCC port relies on. Without `SOL_PI_DIR` it clones NVlabs/SoL-Pi at `2195680` and runs `npm ci`.

## Results

Run on 2026-10-25.

| | |
|---|---|
| PiG | `0.5.3+1.4.0.74f46864` |
| Go | `go1.27.3 darwin/arm64` |
| Machine | `Darwin arm64` |

`scripts/live/verify.sh`:

```text
PiG 0.5.3+1.4.0.74f46864 · work directory …/scripts/live/work/20261025-020958
1. validate-only: sol-pig builds and registers
2. fake-provider session: answered 42; obs_recall and its snippet reached the request
3. idle footprint: 33 MiB across 2 processes (pig + extension runner)
4. scripted endpoint: placeholder on the third send, obs_recall returned the bytes
5. action fusion: then_run ran inside write and edit; built-in parity unchanged
6. reducer: a make failure became a verified receipt through the scripted model
7. occ: a completed plan step triggered native compaction and the task resumed
verify.sh: ok
```

`scripts/live/sol-pi-under-pig.sh` (Node v26.12.1, `SOL_PI_DIR` pointing at a SoL-Pi checkout at `2195680`):

```text
sol-pi-under-pig: PiG runs SoL-Pi's compaction sequence
```

## Footprint

Idle memory for 10 instances, from the earlier measurements made while planning the port:

| Setup | 10 idle instances |
|---|---|
| Bare PiG | 116 MiB |
| PiG with a minimal Go extension | 202 MiB |
| PiG with SoL-Pi under Node | 1,407 MiB |

Step 3 above measures one SoL-PiG instance (PiG plus its extension runner) at 33 MiB on this run. On macOS the script reads `footprint` (physical footprint); elsewhere it falls back to RSS, so numbers from CI are not comparable.

## CI

[`.github/workflows/ci.yml`](../.github/workflows/ci.yml) runs on pushes to `main` and on pull requests. On `ubuntu-latest` it runs `go vet` and `go test` for the extension and for the scripted endpoint, installs PiG 0.5.3 with `npm install -g @pi-in-go/pig@0.5.3`, and runs `scripts/live/verify.sh`. The `sol-pi-under-pig.sh` pre-check is not part of CI.
