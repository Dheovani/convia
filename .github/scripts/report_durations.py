"""
Say which tests took the time, and make a `-json` run readable again.

This is `M24-015`. A suite that takes eight minutes is eight minutes of
somebody's attention every time they push, and the usual way that gets worse is
one test at a time, each addition too small to argue with. Nothing in Convia
made the distribution visible, so the only number anybody saw was the total --
which says a suite is slow and nothing about where.

It reads `go test -json` from standard input. CI runs the suite that way so
this can summarise it, and the cost of that is a console full of JSON, so this
prints the failures back in a form somebody can read.

**It reports; it does not judge.** A slow test is a judgement, and a threshold
nobody chose is a threshold somebody silences, so the `go test` step alone
decides whether the run passed. This always exits zero.
"""

import collections
import json
import sys

# How many rows are worth reading. A list nobody scrolls is a list nobody uses.
SHOWN = 12

# Below this, a test is not why the suite is slow.
INTERESTING_SECONDS = 0.5

# How much of a failure's output to repeat: enough for the assertion and its
# context, short of pasting a goroutine dump per failure.
OUTPUT_LINES = 40


def events():
    for line in sys.stdin:
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            yield json.loads(line)
        except json.JSONDecodeError:
            continue


def main():
    packages = {}
    tests = {}
    failures = []
    output = collections.defaultdict(list)

    for event in events():
        action = event.get("Action")
        package = event.get("Package", "?")
        test = event.get("Test")
        name = "{}.{}".format(package, test) if test else package

        if action == "output":
            output[name].append(event.get("Output", ""))
            continue

        if action not in {"pass", "fail"}:
            continue

        elapsed = event.get("Elapsed")
        if elapsed is not None:
            if test:
                tests[name] = elapsed
            else:
                packages[package] = elapsed

        # A package fails without any test failing when it does not build, or
        # when something panicked and took the process with it. That output is
        # the only thing saying why, and it arrives against the package.
        if action == "fail":
            if test or not any(row.startswith(package + ".") for row in failures):
                failures.append(name)

    if not packages and not tests:
        print("nothing to report: no test events were read")
        return 0

    if failures:
        plural = "failure" if len(failures) == 1 else "failures"
        print("{} {}".format(len(failures), plural))
        print()
        for name in failures:
            print("--- " + name)
            for line in output[name][:OUTPUT_LINES]:
                print("    " + line.rstrip())
            print()

    total = sum(packages.values())
    print("the suite took {:.1f}s across {} packages and {} tests".format(
        total, len(packages), len(tests)))
    print()

    print("slowest packages")
    for name, elapsed in sorted(packages.items(), key=lambda row: -row[1])[:SHOWN]:
        share = (elapsed / total * 100) if total else 0
        print("  {:7.2f}s  {:4.1f}%  {}".format(elapsed, share, name))

    slow = [row for row in tests.items() if row[1] >= INTERESTING_SECONDS]
    print()
    print("slowest tests (of {} over {}s)".format(len(slow), INTERESTING_SECONDS))
    for name, elapsed in sorted(slow, key=lambda row: -row[1])[:SHOWN]:
        print("  {:7.2f}s  {}".format(elapsed, name))

    return 0


if __name__ == "__main__":
    sys.exit(main())
