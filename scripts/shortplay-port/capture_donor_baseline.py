#!/usr/bin/env python3
"""Run the S01 donor capture against the pinned shortplay worktree.

The donor test must live in backend/internal/app because it exercises the
package-private canvas command helpers.  This script copies the checked-in
template into that package, runs one test, and removes the temporary file in a
finally block.
"""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import shutil
import subprocess
import sys


DONOR_COMMIT = "13731aae6c4cefadc2a1719e3e7e59b22e249f94"
TEST_NAME = "TestS01CaptureDonorBehavior"
TEST_FILE = "zz_shortplay_s01_capture_test.go"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--donor-root", required=True, help="path to the yyvan1/open-ai-canvas worktree")
    parser.add_argument("--out", required=True, help="directory for generated JSON fixtures")
    parser.add_argument("--keep", action="store_true", help="keep the temporary test beside the donor tree")
    parser.add_argument("--allow-other-commit", action="store_true", help="do not require the pinned donor commit")
    return parser.parse_args()


def run(
    *args: str,
    cwd: Path,
    check: bool = True,
    env: dict[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(args, cwd=cwd, check=check, text=True, capture_output=True, env=env)


def main() -> int:
    args = parse_args()
    donor = Path(args.donor_root).resolve()
    out = Path(args.out).resolve()
    template = Path(__file__).with_name("donor_capture_test.go.txt")
    target = donor / "backend" / "internal" / "app" / TEST_FILE
    if not (donor / "backend" / "go.mod").exists():
        raise SystemExit(f"donor backend not found: {donor / 'backend'}")
    if not template.exists():
        raise SystemExit(f"capture template missing: {template}")

    status = run("git", "status", "--porcelain", cwd=donor).stdout.strip()
    if status:
        raise SystemExit("donor worktree is not clean; refusing to inject a temporary test")
    head = run("git", "rev-parse", "HEAD", cwd=donor).stdout.strip()
    if not args.allow_other_commit and head != DONOR_COMMIT:
        raise SystemExit(f"donor HEAD is {head}, expected {DONOR_COMMIT}")

    out.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(template, target)
    env = os.environ.copy()
    env["SHORTPLAY_S01_OUT"] = str(out)
    env["SHORTPLAY_DONOR_COMMIT"] = head
    try:
        result = run(
            "go", "test", "-count=1", "-run", f"^{TEST_NAME}$", "-v", "./internal/app",
            cwd=donor / "backend", check=False, env=env,
        )
        sys.stdout.write(result.stdout)
        sys.stderr.write(result.stderr)
        if result.returncode != 0:
            return result.returncode
    finally:
        if not args.keep:
            try:
                target.unlink()
            except FileNotFoundError:
                pass
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
