"""
Refuse a dependency whose licence Convia cannot accept.

This is `M23-008`. It reads a CycloneDX bill of materials produced with
`-licenses` and fails the build on anything outside the policy, so a licence
that would bind Convia's own terms is caught when it arrives rather than when
somebody asks.

It reads the **module graph** rather than one binary's bill of materials. A
dependency that only the desktop application links is still a dependency of
this repository, and a policy that saw only the service would miss it.
"""

import json
import sys

"""
The policy, which is `AGENTS.md`'s rule written out.

That rule says BSD, MIT and Apache-2.0, and names GPL, LGPL and AGPL as the
thing it exists to keep out. The four beyond it are listed rather than assumed:
each is **more** permissive than MIT, none is copyleft of any strength, and
each is here because a dependency Convia already has carries it. Weak copyleft
is deliberately absent -- MPL-2.0 and EPL are not on this list, and a
dependency carrying one is a decision for a person rather than for this script.
"""
ALLOWED = {
    "MIT",
    "Apache-2.0",
    "BSD-2-Clause",
    "BSD-3-Clause",
    "BSD-Source-Code",
    "ISC",
    "0BSD",
    "Unlicense",
}

"""
Modules whose licence was read by hand because the detector cannot read it.

Each entry is a claim somebody checked, not a way of silencing the check: the
module is still refused if it disappears from this file, and adding one means
opening the LICENSE file and writing down what it says.

- `github.com/pressly/goose/v3` is MIT. The file is an ordinary MIT text; what
  defeats the detector is the "Original work / Modified work" preamble above
  the copyright lines.
- `github.com/jchv/go-winloader` is ISC, in `LICENSE.md` rather than `LICENSE`.
"""
VERIFIED_BY_HAND = {
    "github.com/pressly/goose/v3": "MIT",
    "github.com/jchv/go-winloader": "ISC",
}


def licences_of(component):
    """
    Read a component's licences from wherever this generator put them.

    cyclonedx-gomod reports a detected licence as evidence rather than as an
    assertion, because it inferred it from a file instead of being told. Both
    places are read so that a generator that changes its mind does not silently
    turn this check into one that passes on everything.
    """
    found = []
    for holder in (component.get("evidence", {}).get("licenses", []),
                   component.get("licenses", [])):
        for entry in holder:
            licence = entry.get("license") or {}
            name = licence.get("id") or licence.get("name") or entry.get("expression")
            if name:
                found.append(name)
    return found


def main(path):
    with open(path, encoding="utf-8") as handle:
        document = json.load(handle)

    components = document.get("components", [])
    if not components:
        print(f"{path} lists no components, which means the scan did not run")
        return 1

    refused = []
    unreadable = []

    for component in components:
        name = component.get("name", "?")
        found = licences_of(component)

        if not found:
            if name in VERIFIED_BY_HAND:
                found = [VERIFIED_BY_HAND[name]]
            else:
                unreadable.append(name)
                continue

        for licence in found:
            if licence not in ALLOWED:
                refused.append(f"{name}: {licence}")

    for line in refused:
        print(f"refused licence  {line}")
    for name in unreadable:
        print(f"licence unreadable  {name}: open its LICENSE file, then add it "
              f"to VERIFIED_BY_HAND in this script")

    if refused or unreadable:
        print(f"\n{len(components)} modules checked, "
              f"{len(refused)} refused, {len(unreadable)} unreadable")
        return 1

    print(f"{len(components)} modules checked, every licence is within the policy")
    return 0


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("usage: check_licenses.py <cyclonedx.json>")
        sys.exit(2)
    sys.exit(main(sys.argv[1]))
