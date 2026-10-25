# Changelog

## 0.1.0 — 2026-10-25

First release, published to npm as `sol-pig`. A port of [SoL-Pi](https://github.com/NVlabs/SoL-Pi) (NVIDIA, MIT) at commit 2195680; model-visible texts are byte-identical to SoL-Pi's, see docs/compatibility.md for the differences.

- feat(action-fusion): `edit` and `write` take an optional `then_run` command that runs through PiG's bash tool in the same tool call.
- feat(observation-pack): large tool results become placeholders after two full sends, with exact paged recall through `obs_recall`.
- feat(reducer): long diagnostic logs become receipts whose quotes are verified byte for byte against the archived log.
- feat(occ): completed `update_plan` steps trigger PiG's native compaction when the economic check or window pressure selects it.
- test(live): `scripts/live/verify.sh` checks all four mechanisms in a real PiG process at zero spend; `scripts/live/sol-pi-under-pig.sh` runs SoL-Pi itself under PiG.
