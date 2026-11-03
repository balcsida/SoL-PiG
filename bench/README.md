# Measuring SoL-PiG on real sessions

Two tools for the question "does SoL save money in my own sessions, on the model I actually use". Neither spends money. Both print aggregates only: no session ids, paths, project names or message content.

## Replay recorded sessions

```bash
python3 -I bench/replay_sessions.py \
  --store "PiG personal=~/.pig/agent-personal/sessions" \
  --store "PiG work=~/.pig/agent/sessions" \
  --out bench/out/replay.md
```

For every assistant message (one provider request) the script rebuilds the model context from the session tree (active branch, compaction, context edits), estimates each block at four characters per token, and scales the estimates to the request's recorded prompt tokens. It then prices the request under a prefix-cache model: tokens before the first block that changed since the previous request are cache reads, the rest cache writes, and a request whose recorded cache read was zero is cold. Cache-warming requests (`usage` entries) are a full read of the prompt at its size under each policy.

The same session is priced under several ObservationPack policies: plain Pi (nothing replaced), SoL-Pi as shipped (a result over 10 KiB is replaced from its third send), [NVlabs/SoL-Pi#121](https://github.com/NVlabs/SoL-Pi/pull/121) (second send), placeholder from the first send, and second send at a 4 KiB threshold. The recorded policy is detected from the session's system message (`obs_recall` in the tool declarations means ObservationPack was on), and `actual` prices the session under that policy: its gap to the recorded cost is the model's error, reported per store as the calibration line.

Prices are each request's recorded per-token rates when the session carries a cost (litellm sessions do), else Pi's model registry list prices (`--registry`, default: the registry data in a SoL-Pi checkout's `node_modules`). claude-bridge sessions record no cost, so their dollars are Anthropic list prices for the same tokens.

What it cannot see: behaviour changes. A placeholder the model would have recalled, or a compaction that changes later turns, is not in the recording. Action Fusion and the reducer only get opportunity counts: edit/write calls followed by a bash call in the next turn, and diagnostic bash output of 4 KiB or more.

## Alternate SoL on and off by day

```bash
python3 -I bench/ab_toggle.py ~/.pig/agent/sol-pi.json   # from the launcher, before every start
```

On an even day of the year the config is the saved "on" file (`sol-pi.on.json` next to it, taken from the current config the first time), on an odd day every mechanism is `false`. The arm goes to `~/.pig/state/sol-ab.log` once per day. `touch sol-ab.stop` next to the config ends the alternation and restores "on". A failure leaves the config alone.

The replay script tells the arms apart from the session itself (the system message lists `obs_recall` only when ObservationPack was on), so after a few weeks the recorded costs of the two arms can be compared with the same report, and the replay's modelled saving checked against a real difference.
