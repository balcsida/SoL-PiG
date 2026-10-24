# SoL-PiG

SoL-PiG brings [SoL-Pi](https://github.com/NVlabs/SoL-Pi)'s four token-efficiency mechanisms (Action Fusion, ObservationPack, the Evidence-Preserving Reducer and Online Context Compact) to [PiG](https://github.com/MichaelKinsy/PiG) as a native Go extension, so each PiG instance pays a few MiB for them instead of a Node process. It is a port of SoL-Pi, which NVIDIA released under the MIT license for the Pi coding agent; SoL-PiG is not an NVIDIA or PiG project. Every mechanism is opt-in and off by default, configured by the same `sol-pi.json` SoL-Pi reads. Install SoL-PiG or SoL-Pi in a PiG, not both: they register the same tools and share the runtime directory.

```bash
pig install git:github.com/balcsida/SoL-PiG
```

## Development

```bash
cd extensions/sol-pig && go vet ./... && go test ./...   # unit and fake-host tests, zero spend
scripts/live/verify.sh                                    # real pig 0.5.3: fake provider + scripted endpoint, zero spend
pig install "$PWD" --validate-only --json                 # does PiG accept the package?
```
