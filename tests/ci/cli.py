"""Discover compiled CLI scenarios and run one complete, deterministic shard."""
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys

REPO = Path(__file__).resolve().parents[2]


def shard(names, index, count=3):
    if not 1 <= index <= count or len(names) != len(set(names)):
        raise ValueError("Invalid shard index or duplicate scenario names")
    selected = sorted(names)[index - 1::count]
    if not selected:
        raise ValueError("No scenarios selected; refusing an empty successful shard")
    return selected


def main(index):
    artifacts = REPO / "test-artifacts/ci"
    artifacts.mkdir(parents=True, exist_ok=True)
    binary = artifacts / f"cli-{index}.test"
    go = shlex.split(os.environ.get("GO", "go"))
    subprocess.run(go + ["test", "-tags=e2e", "-c", "-o", str(binary), "./tests/e2e"], cwd=REPO, check=True)
    cwd = REPO / "tests/e2e"
    listed = subprocess.check_output([str(binary), "-test.list=^Test"], cwd=cwd, text=True)
    names = [line for line in listed.splitlines() if re.fullmatch(r"Test\w+", line)]
    selected = shard(names, index)
    print(f"CLI shard {index}/3: {len(selected)} of {len(names)} scenarios", flush=True)
    print("\n".join(selected), flush=True)
    env = dict(os.environ, KEYPARCEL_GO=shlex.join(go))
    subprocess.run([str(binary), "-test.v", "-test.timeout=15m", "-test.parallel=2",
                    "-test.run=^(" + "|".join(map(re.escape, selected)) + ")$"], cwd=cwd, env=env, check=True)


if __name__ == "__main__":
    main(int(sys.argv[1]))
