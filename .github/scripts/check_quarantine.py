"""
Refuse a test that is switched off without an owner, an issue, and a date.

This is `M24-014`. Quarantine is how a flaky test stops blocking a release, and
it is also how a flaky test stops being anybody's problem: the skip lands, the
build goes green, and nothing ever says the coverage left. **A quarantine
without a deadline is a deletion nobody had to argue for**, and the test keeps
existing so that nobody notices it went.

So a quarantine here carries three things, and this refuses it otherwise:

- an **owner**, because a test belonging to everybody belongs to nobody;
- an **issue**, so the flake is described somewhere other than a skip reason;
- an **until** date, which is the part with teeth. It has to be in the future,
  or the build fails until somebody fixes the test, extends the date on
  purpose, or deletes the test and admits the coverage is gone. All three are
  fine. Silence is not.

    // QUARANTINE(owner: @someone, issue: #412, until: 2026-11-15)
    // The relay races with Redis key expiry under -shuffle.
    t.Skip("quarantined: see QUARANTINE above")

**Every other skip has to be recognisable too.** Convia's tests skip when the
database is unset, and that is a different thing from quarantine, so this only
lets through the shapes listed in ALLOWED below. A skip that matches none of
them fails -- which means hiding a flake behind `t.Skip("TODO")` needs somebody
to widen the list in this file, in a diff a reviewer reads. That is the point.
"""

import datetime
import pathlib
import re
import sys

# A skip nobody would call quarantine. Each entry says what it is for; adding
# one is a deliberate act, which is the whole mechanism.
ALLOWED = [
    # The dependency this package needs was not declared, so the test cannot
    # run at all. CI always declares it.
    (re.compile(r"set %s"), "a dependency is unset"),
    (re.compile(r"set %s(, %s)*,? and %s"), "several dependencies are unset"),
    # Behaviour that exists on some operating systems and not others. These
    # match the whole call, because the platform is usually an argument
    # rather than part of the reason.
    (re.compile(r"\bGOOS\b|\bwindows\b|\bdarwin\b|\blinux\b"), "platform"),
    # The compiled interface is absent, which is a build step rather than a bug.
    (re.compile(r"no bundle is compiled in"), "the interface is not built"),
]

SKIP = re.compile(r"\bt\.Skipf?\(")

MARKER = re.compile(
    r"QUARANTINE\(\s*owner:\s*(?P<owner>[^,]+?)\s*,"
    r"\s*issue:\s*(?P<issue>[^,]+?)\s*,"
    r"\s*until:\s*(?P<until>\d{4}-\d{2}-\d{2})\s*\)"
)

# How far above the skip the marker may sit. Enough for a sentence of reason,
# not enough to belong to a different test.
LOOKBEHIND = 8

# A deadline nobody will reach is not a deadline.
LONGEST_DAYS = 90


def reason(line):
    quoted = re.search(r'"((?:[^"\\]|\\.)*)"', line)
    return quoted.group(1) if quoted else ""


def check(path, lines, today):
    complaints = []

    for number, line in enumerate(lines):
        if not SKIP.search(line):
            continue

        where = "{}:{}".format(path.as_posix(), number + 1)
        said = reason(line)
        quarantined = "quarantine" in said.lower()

        marker = None
        for above in lines[max(0, number - LOOKBEHIND):number]:
            found = MARKER.search(above)
            if found:
                marker = found

        if marker is None:
            if quarantined:
                complaints.append(
                    "{}  says it is quarantined and carries no QUARANTINE marker".format(where))
            elif not any(shape.search(line) for shape, _ in ALLOWED):
                complaints.append(
                    '{}  skips for a reason this gate does not recognise: "{}"'.format(where, said))
            continue

        until = datetime.date.fromisoformat(marker.group("until"))
        if until < today:
            complaints.append(
                "{}  quarantined until {}, which has passed. Fix it, extend the date "
                "on purpose, or delete the test.".format(where, until))
        elif (until - today).days > LONGEST_DAYS:
            complaints.append(
                "{}  quarantined until {}, more than {} days out. A deadline nobody "
                "will reach is not a deadline.".format(where, until, LONGEST_DAYS))

    return complaints


def main():
    today = datetime.date.today()
    complaints = []
    quarantined = 0

    for path in sorted(pathlib.Path(".").rglob("*_test.go")):
        if "node_modules" in path.parts:
            continue
        lines = path.read_text(encoding="utf-8", errors="replace").split("\n")
        quarantined += len(MARKER.findall("\n".join(lines)))
        complaints.extend(check(path, lines, today))

    for complaint in complaints:
        print(complaint)

    if complaints:
        print("\n{} skips are not accounted for. See the top of this script.".format(
            len(complaints)))
        return 1

    if quarantined:
        print("{} tests are quarantined, each with an owner, an issue, and a date".format(
            quarantined))
    else:
        print("no tests are quarantined, and every skip is a declared shape")
    return 0


if __name__ == "__main__":
    sys.exit(main())
