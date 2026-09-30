"""
Refuse a package that ships code and has no test at all.

This is `M24-010`, and it is deliberately not a percentage.

A percentage gate rewards testing whatever is cheapest to cover and says
nothing about whether the covered part is the part that matters — an eighty
per cent that is all getters passes, and a sixty per cent that is every
invariant fails. Convia has the coverage numbers recorded per package in
`docs/testing.md` for the judgement a person makes; this is for the judgement
nobody should have to make.

**What it catches is the failure that actually happened, twice.**
`internal/secret` is the one place keeping one credential family from being
accepted as another, and it had no tests until `M23-014`. `internal/transaction`
decides whether an event is announced for a change that rolled back, and it had
none until `M24-009`. Both were found by somebody looking, and both are small
enough to look like plumbing — which is exactly why nobody looked sooner.

A package with no tests is not a number to argue about. It is a package where
nothing would notice a change.
"""

import json
import subprocess
import sys

# Generated code, and packages that exist to be embedded rather than run.
EXEMPT = {
    "convia/cmd/convia-desktop/icon",
}


def packages():
    listed = subprocess.run(
        ["go", "list", "-json", "./..."],
        capture_output=True, text=True,
    )

    # Without this, a toolchain that cannot load the module raises a traceback
    # ending in CalledProcessError, and the reason -- which `go` already said
    # plainly on stderr -- is the one line not printed.
    if listed.returncode != 0:
        print("go list failed, so this gate could not run:")
        print(listed.stderr.strip())
        sys.exit(1)

    decoder = json.JSONDecoder()
    text = listed.stdout.lstrip()
    while text:
        described, index = decoder.raw_decode(text)
        yield described
        text = text[index:].lstrip()


def main():
    untested = []

    for described in packages():
        path = described["ImportPath"]
        if path in EXEMPT:
            continue
        if not described.get("GoFiles"):
            continue
        if described.get("TestGoFiles") or described.get("XTestGoFiles"):
            continue
        untested.append(path)

    for path in sorted(untested):
        print(f"no tests at all  {path}")

    if untested:
        print(f"\n{len(untested)} packages ship code that nothing would notice a change to.")
        print("Add a test, or add the package to EXEMPT in this script with the reason.")
        return 1

    print("every package that ships code has tests")
    return 0


if __name__ == "__main__":
    sys.exit(main())
