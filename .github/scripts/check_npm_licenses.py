"""
Refuse a npm dependency whose licence Convia cannot accept.

This is `M19-012`, and it closes a gap rather than adding a precaution: every Go
module's licence has been checked since `M23-008`, and the two hundred npm
packages the workspace pulls were checked by nothing at all. `AGENTS.md`'s rule
is about what Convia depends on, not about which language it is written in.

**It holds what ships to the policy and only records what builds**, which is the
distinction `security.yml` already makes for `npm audit` and for the same
reason: what the bundle carries runs in somebody's browser alongside their
session, and what a build tool carries never reaches anybody. A CSS compiler
under weak copyleft is a tool Convia uses; a library under one, compiled into
the page, is a term Convia would be passing on.

So the production tree is refused on anything outside the policy, and everything
else is listed with its licence so that a change there is a thing somebody can
see rather than a thing nobody looked at.

The policy itself is imported rather than copied. Two lists of accepted licences
are two policies, and they drift.
"""

import json
import pathlib
import subprocess
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))

from check_licenses import ALLOWED  # noqa: E402

"""
Packages whose licence field says something a parser cannot act on.

Each is a claim somebody checked, not a way of silencing the check. A workspace
member of Convia's own is not a third-party dependency and carries the
repository's licence by being part of it.
"""
OURS = {"convia-web", "convia-sdk", "convia"}


def production():
    """The packages that end up in what Convia ships, workspaces included."""
    listed = subprocess.run(
        # `--long` is what carries the licence and the path: without it the
        # tree is versions alone, and every package reads as unlicensed.
        ["npm", "ls", "--omit=dev", "--all", "--long", "--json"],
        capture_output=True, text=True, encoding="utf-8", errors="replace",
        shell=(sys.platform == "win32"),
    )

    # `npm ls` exits non-zero on peer-dependency complaints while still writing
    # the tree, so the output is what decides rather than the status.
    if not listed.stdout.strip():
        print("npm ls produced nothing, so this gate could not check anything:")
        print(listed.stderr.strip())
        sys.exit(1)

    tree = json.loads(listed.stdout)
    found = {}

    def walk(node):
        for name, info in (node.get("dependencies") or {}).items():
            version = info.get("version")
            if (name, version) in found:
                continue
            found[(name, version)] = info
            walk(info)

    walk(tree)
    return found


def licenceOf(described):
    """
    Whatever the package says about itself, as one string.

    `npm ls --long` carries the manifest, so nothing is read off disk -- which
    also means a package resolved from somewhere unusual is read the same way
    as any other.
    """
    stated = described.get("license") or described.get("licenses")
    if isinstance(stated, dict):
        return stated.get("type")
    if isinstance(stated, list):
        kinds = [one.get("type") if isinstance(one, dict) else str(one) for one in stated]
        return " OR ".join(kind for kind in kinds if kind)
    return stated


def acceptable(stated):
    """
    Whether a licence expression is inside the policy.

    An `OR` is a choice the consumer makes, so one acceptable option is enough.
    An `AND` is not a choice, so every part has to be acceptable -- and the
    parenthesised forms SPDX allows are left to a person rather than guessed at.
    """
    if stated is None:
        return False

    expression = stated.strip()

    """
    A whole expression in brackets is the same expression.

    SPDX brackets a compound routinely -- `(Apache-2.0 AND BSD-3-Clause)` is how
    one of Convia's own dependencies states itself -- and refusing every bracket
    refuses well-formed expressions whose every part is accepted. What is still
    refused is a bracket that is not the whole of it, such as `(A OR B) AND C`:
    that needs precedence, and guessing at precedence in a licence is worse than
    asking somebody.
    """
    while expression.startswith("(") and expression.endswith(")"):
        inner = expression[1:-1]
        if inner.count("(") != inner.count(")"):
            return False
        expression = inner.strip()

    if "(" in expression or ")" in expression:
        return False

    if " OR " in expression:
        return any(acceptable(one) for one in expression.split(" OR "))
    if " AND " in expression:
        return all(acceptable(one) for one in expression.split(" AND "))

    return expression.removesuffix(" WITH LLVM-exception") in ALLOWED


"""
What the rules above are supposed to answer, checked when this runs.

The reading of an expression is the whole of this gate's judgement, and it is
the part with no output of its own: a rule that quietly started accepting
`GPL-3.0` would pass every dependency and say so cheerfully. These are run by
CI alongside the check itself, which is why they are here rather than in a test
file -- `.github/scripts` has no test runner, and inventing one for a table is
heavier than the table.
"""
RULES = [
    ("MIT", True),
    ("Apache-2.0", True),
    ("(Apache-2.0 AND BSD-3-Clause)", True),
    ("MIT OR GPL-3.0", True),
    ("Apache-2.0 WITH LLVM-exception", True),
    ("GPL-3.0", False),
    ("AGPL-3.0-only", False),
    ("LGPL-3.0", False),
    ("MPL-2.0", False),
    ("MIT AND GPL-3.0", False),
    ("(MIT OR GPL-3.0) AND Apache-2.0", False),
    ("CC-BY-4.0", False),
    (None, False),
    ("", False),
]


def rulesHold():
    wrong = [
        "{!r} read as {}, wanted {}".format(stated, acceptable(stated), want)
        for stated, want in RULES
        if acceptable(stated) != want
    ]

    for line in wrong:
        print("the licence rules are wrong: " + line)

    if wrong:
        return False

    print("the licence rules read {} expressions the way they should".format(len(RULES)))
    return True


def main():
    if not rulesHold():
        return 1

    refused = []
    shipped = production()

    for (name, version), described in sorted(shipped.items()):
        if name in OURS:
            continue
        stated = licenceOf(described)
        if not acceptable(stated):
            refused.append("{}@{}: {}".format(name, version, stated or "no licence stated"))

    for line in refused:
        print("refused  " + line)

    if refused:
        print()
        print("{} packages that reach somebody's browser carry a licence outside the policy.".format(
            len(refused)))
        print("The policy is ALLOWED in check_licenses.py, and widening it is a decision for a person.")
        return 1

    print("every package Convia ships carries a licence the policy accepts "
          "({} checked)".format(len(shipped)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
