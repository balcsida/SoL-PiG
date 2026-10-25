# Third-party notices

SoL-PiG is a port of SoL-Pi onto PiG's Go extension SDK. This file names the third-party code present in the repository. `UPSTREAM.md` lists every ported or copied file with its source and commit. Each file derived from a third party names its source in a header comment.

## SoL-Pi

Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
https://github.com/NVlabs/SoL-Pi — MIT. Ported from commit `2195680`.

The packages under `extensions/sol-pig` (`config`, `paths`, `savings`, `actionfusion`, `observationpack`, `reducer`, `occ` and the loader in `extension.go`) are Go ports of SoL-Pi's TypeScript, except `internal/actionfusion/prepare.go`. The model-facing texts are SoL-Pi's. NVIDIA's copyright and the MIT permission notice apply to these files.

## PiG

Copyright Michael Kinsy
https://github.com/MichaelKinsy/PiG — MIT. Code copied from tag `v0.5.3`: `extensions/sol-pig/internal/actionfusion/prepare.go`, a port of `internal/codingagent/tools/edit.go` (the edit tool's argument normaliser). It is the only file derived from PiG.

PiG is itself derived from Pi (https://github.com/earendil-works/pi, Copyright (c) 2025 Mario Zechner, MIT) and carries Pi's notice; that notice applies to `prepare.go` as well. The MIT permission notice in LICENSE is the notice under which this PiG- and Pi-derived code is used.

## PiG extension SDK (dependency)

`github.com/MichaelKinsy/PiG/extensions/sdk` v0.5.3 (MIT, Copyright Michael Kinsy) is a Go module dependency, resolved by `go.mod`. None of its source is copied into this repository.

Nothing else is copied.
