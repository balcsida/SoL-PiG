#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright (c) 2026 SoL-PiG contributors
# SPDX-License-Identifier: MIT
"""Alternate SoL-PiG on and off by calendar day, for an on/off comparison of real sessions.

    python3 -I bench/ab_toggle.py ~/.pig/agent/sol-pi.json

Run from the launcher before every start. On an even day of the year the config is the
"on" file (sol-pi.on.json next to it, taken from the current config the first time), on an
odd day every mechanism is false. The arm is appended to ~/.pig/state/sol-ab.log once per
day. A file named sol-ab.stop next to the config stops the alternation and restores "on".
Any failure leaves the config alone: a measurement toggle must never stop the agent.
"""

import datetime
import json
import os
import sys

MECHANISMS = ("actionFusion", "observationPack", "evidencePreservingReducer", "onlineContextCompact")


def main(path: str) -> None:
    path = os.path.realpath(os.path.expanduser(path))  # the profile's copy is a symlink to the real file
    directory = os.path.dirname(path)
    on_path = os.path.join(directory, "sol-pi.on.json")
    current = json.load(open(path))
    if os.path.exists(on_path):
        on = json.load(open(on_path))
    elif any(current.get(key) for key in MECHANISMS):
        on = current
        with open(on_path, "w") as f:
            json.dump(on, f, indent=2)
    else:
        return  # nothing is enabled and no saved "on" config: nothing to alternate
    today = datetime.date.today()
    stop = os.path.exists(os.path.join(directory, "sol-ab.stop"))
    arm = "on" if stop or today.timetuple().tm_yday % 2 == 0 else "off"
    desired = on if arm == "on" else {**on, **{key: False for key in MECHANISMS}}
    if current != desired:
        tmp = f"{path}.{os.getpid()}.tmp"
        with open(tmp, "w") as f:
            json.dump(desired, f, indent=2)
        os.chmod(tmp, os.stat(path).st_mode & 0o777)
        os.replace(tmp, path)
    if stop:
        return
    log = os.path.expanduser("~/.pig/state/sol-ab.log")
    os.makedirs(os.path.dirname(log), exist_ok=True)
    line = f"{today.isoformat()} {arm}\n"
    try:
        last = open(log).readlines()[-1]
    except (OSError, IndexError):
        last = ""
    if last != line:
        with open(log, "a") as f:
            f.write(line)


if __name__ == "__main__":
    try:
        main(sys.argv[1])
    except Exception:
        pass
