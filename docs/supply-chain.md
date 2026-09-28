# Supply chain

This is `M23-007` through `M23-012` — **what Convia is built from, and what would notice if that changed underneath it.** It is a companion to [`threat-model.md`](threat-model.md), which names the boundaries a request crosses, and to [`data-protection.md`](data-protection.md), which names what is worth taking. This one is about the code that arrives without anybody asking for it.

The threat model lists supply chain as out of scope. It is not any more.

## What runs, and where

| Check | Where | Fails the build on |
| --- | --- | --- |
| Secret scanning | `security.yml`, every push and PR | a credential anywhere in the history |
| Go vulnerabilities | `security.yml`, plus Mondays | a reachable known vulnerability |
| Interface dependencies | `security.yml`, plus Mondays | a `moderate` in the bundle, a `high` in the build |
| CodeQL, Go and TypeScript | `security.yml`, plus Mondays | a `security-extended` finding |
| License policy | `security.yml` | a dependency outside the policy below |
| Bill of materials | `security.yml` | the scan not running at all |
| Image vulnerabilities | `container.yml` | a fixable `HIGH` or `CRITICAL` in the image |

The weekly schedule matters more than it looks. A dependency that was clean when it was merged does not stay clean, and nothing about the repository changes on the day somebody publishes an advisory — so a check that only runs on a push is a check that reports the news late.

**Dependabot proposes updates weekly** for GitHub Actions, Go modules and the interface's npm packages, grouped so that a week's updates arrive as one pull request per ecosystem rather than as twenty.

## The two halves, and which was covered

Convia is written in two languages, and until `M23-016` the checks only knew about one. `govulncheck`, CodeQL and the module licence policy all stopped at the Go boundary; the interface's dependency tree — the half that **runs in somebody's browser, on the same origin as their session** — had no vulnerability scan, no static analysis and no update automation. The image scan did not reach it either, because the bundle is compiled into the binary before the image is built.

That is the wrong way round, and it is closed rather than noted:

- **`npm audit` at two levels.** What the bundle carries is held to `moderate`, because it reaches a person's browser. What only the build and the tests carry is held to `high`, which still refuses a build tool that takes arbitrary code while letting a path traversal in a test runner wait for its upstream fix.
- **CodeQL analyses TypeScript as well as Go**, from source. It is the half that renders other people's names and messages into a page.
- **Dependabot watches `web/`**, which it did not.

## Secrets

**The scan reads the whole history, not the working tree.** A credential that was committed and then deleted is still in the history, still works, and is exactly the case nobody noticed at the time. The checkout is therefore unshallow, which is the only reason this catches anything a careful reviewer would not.

Two rules are Convia's own, on top of the defaults that carry everybody else's secrets:

- **A credential of any of the four families**, presented whole. What this catches is not an attacker: it is somebody pasting a working key into a commit while reproducing a problem, which is how credentials actually leak.
- **A value assigned to a secret-bearing variable.** A media API secret, a database password and a LiveKit key are long opaque strings with no shape of their own, so the only thing that can be matched is the one place they are ever written down.

`.env` is **deliberately not allowlisted**. It is gitignored, which is what keeps a real one out of the history — and if one is ever committed past that, this is what says so.

### The gap, stated rather than hidden

**Fixtures, the published contract and the documentation are exempt from every rule.** A fixture and a working key are indistinguishable, because the format is designed so that a key looks like a key — so no rule can fire on one and stay quiet on the other.

What makes that acceptable is narrow and worth naming: those three are the only places such strings belong, all three are reviewed as code, and a Convia credential that does leak is revocable through [`runbooks/credential-revocation.md`](runbooks/credential-revocation.md). What would close it properly is fixtures that **cannot be valid credentials at all** — a reserved shape the parser refuses — which is a change to fifteen files and their tests rather than a change to a scanner's configuration.

### What CI cannot do

**Push protection is a repository setting**, not a file. Enable it under *Settings → Code security → Secret protection → Push protection*. Without it the scan is a report after the fact: the secret is already in GitHub's copy of the history by the time the job runs, and rotating it is the only remedy. With it, the push is refused and there is nothing to rotate.

## Licenses

**The policy is `AGENTS.md`'s rule, enforced rather than remembered.** BSD, MIT and Apache-2.0; GPL, LGPL and AGPL are the thing it exists to keep out.

Four more are on the allowlist and are listed rather than assumed — ISC, 0BSD, Unlicense and BSD-Source-Code. Each is **more** permissive than MIT, none is copyleft of any strength, and each is there because a dependency Convia already has carries it. **Weak copyleft is deliberately absent:** MPL-2.0 and EPL are not on the list, and a dependency carrying one is a decision for a person rather than for a script.

**The check reads the module graph, not one binary.** A module only the desktop application links is still a dependency of this repository, and a policy that saw only the service would miss it.

**A license that cannot be read is a failure, not a pass.** Two modules defeat the detector today and are recorded in `check_licenses.py` with what their license file actually says — `goose` is MIT behind an "Original work / Modified work" preamble, `go-winloader` is ISC in `LICENSE.md` rather than `LICENSE`. Adding an entry there means opening the file and writing down what it says; it is a claim somebody checked, not a way of silencing the check.

## Bill of materials

Two are produced on every run and kept as build artefacts:

- **`sbom-convia.json`** — what the release binary is made of. This is the one that answers "was this version affected?" when an advisory names a module.
- **`sbom-modules.json`** — everything in the build list. This is the one the license policy reads.

CycloneDX, in JSON, because it is the format the tooling that consumes these reads. They are generated from the source rather than written by hand, which is the only way a bill of materials stays true.

## Patch deadlines

The clock starts when **CI reports it**, not when the advisory was published. That is the moment somebody here can act, and dating it from publication would make every deadline retroactive.

| Severity | Fixed within | If there is no fix |
| --- | --- | --- |
| `CRITICAL` | 7 days | the week is spent deciding whether to drop the dependency |
| `HIGH` | 30 days | recorded in `TODO.md` with the reason, reviewed monthly |
| `MEDIUM` | 90 days | carried, and it does not fail the build |
| `LOW` | the next dependency update | carried |

**A vulnerability with no fix does not stop the build**, which is why `--ignore-unfixed` is set. A gate that cannot be satisfied is a gate somebody turns off, and a turned-off gate protects nothing. What replaces it is the first column: a `CRITICAL` with no upstream fix is a week of somebody's attention, not a line in a report.

**These are Convia's own deadlines, for a project with no release.** They exist so that the first time one is missed is a decision rather than a discovery, and they are the thing `M23-016` will hang a disclosure process from.

## What this leaves open

- **No signing, no provenance** (`M23-010`). It is blocked rather than skipped: `container.yml` builds an image and throws it away, so there is no published artefact to sign. It arrives with the first release that pushes to a registry.
- **No pinned toolchain hashes beyond `go.sum` and `package-lock.json`.** Actions and container images are pinned by digest; the tools run with `go run` are pinned by version tag, which is weaker.
- **Nothing checks that a Dependabot pull request was reviewed.** The updates arrive; merging them is a person's habit rather than a deadline, and `M23-012`'s clock is what makes a missed one visible.
- **No external review** (`M23-015`), and no intake for somebody outside reporting one (`M23-016`).
