"""
Refuse a generated file that no longer matches what it was generated from.

`sdk/src/contract.ts` is `api/openapi.yaml` in TypeScript, and it is committed
rather than built on demand so that consumers and the type-checker do not need
the generator, and so that a change to the contract shows up as a diff somebody
reads.

Committing it buys that at one cost: **the copy can fall behind the original,
and nothing about it looks wrong.** The types still compile, the tests still
pass, and the SDK describes a Convia that no longer exists. This closes that by
regenerating into a temporary file and comparing -- the same shape as `gofmt -l`
and for the same reason.

It is a check, not a fix: a build that silently rewrote a tracked file would
leave somebody's working tree changed by CI.
"""

import pathlib
import subprocess
import sys
import tempfile

GENERATED = pathlib.Path("sdk/src/contract.ts")
SOURCE = pathlib.Path("api/openapi.yaml")


def main():
    if not GENERATED.exists():
        print("{} is missing. Run `npm run generate`.".format(GENERATED))
        return 1

    with tempfile.TemporaryDirectory() as room:
        fresh = pathlib.Path(room) / "contract.ts"
        built = subprocess.run(
            ["npx", "--no-install", "openapi-typescript", str(SOURCE), "--output", str(fresh)],
            capture_output=True, text=True, cwd=".", shell=(sys.platform == "win32"),
        )

        if built.returncode != 0:
            print("the generator could not run, so this gate could not check anything:")
            print(built.stderr.strip() or built.stdout.strip())
            return 1

        # Line endings differ between a Windows checkout and CI, and that is not
        # a stale file. Everything else is.
        was = GENERATED.read_text(encoding="utf-8").replace("\r\n", "\n")
        now = fresh.read_text(encoding="utf-8").replace("\r\n", "\n")

    if was == now:
        print("{} matches {}".format(GENERATED, SOURCE))
        return 0

    print("{} no longer matches {}.".format(GENERATED, SOURCE))
    print("Run `npm run generate` and commit the result.")
    print()

    # Enough of the difference to recognise what changed, without pasting a
    # thirteen-thousand-line file into the log.
    import difflib

    shown = 0
    for line in difflib.unified_diff(
        was.split("\n"), now.split("\n"),
        fromfile="committed", tofile="regenerated", lineterm="", n=1,
    ):
        print(line)
        shown += 1
        if shown >= 60:
            print("… and more")
            break

    return 1


if __name__ == "__main__":
    sys.exit(main())
