# Security Policy

Convia is real-time communication infrastructure. Its security properties matter to every application that integrates with it, so security reports are treated as a priority over feature work.

## Supported Versions

Convia is in early development and has no released version yet. Only the `main` branch is supported. Once releases exist, this section will record the supported release line and its support window.

| Version | Supported |
| ------- | --------- |
| `main`  | Yes       |

## Reporting a Vulnerability

**Do not open a public issue, pull request, or discussion for a security vulnerability.**

Report it privately through GitHub:

1. Open the **Security** tab of the repository.
2. Choose **Report a vulnerability** to start a private security advisory.
3. Provide the details described below.

Private reporting keeps the discussion, the fix, and the disclosure timeline in one place, visible only to the maintainers and to you until a fix is published.

Please include:

- a description of the issue and the impact you believe it has;
- the affected component, endpoint, or commit;
- reproduction steps, a proof of concept, or a failing request;
- any configuration required to reproduce it;
- whether the issue is already public elsewhere.

Reports written in English are preferred, because all project documentation is maintained in English.

## What to Expect

| Stage                | Target                                                        |
| -------------------- | ------------------------------------------------------------- |
| Acknowledgement      | Within 5 business days                                        |
| Initial assessment   | Within 10 business days, including severity and whether it is accepted |
| Fix or mitigation    | Prioritized by severity; critical issues take precedence over roadmap work |
| Disclosure           | Coordinated with the reporter after a fix or mitigation exists |

Convia is maintained by a very small team, so these are targets rather than contractual guarantees. If a report goes unanswered past the acknowledgement target, please send a reminder through the same advisory.

Reporters are credited in the advisory unless they ask not to be. There is no bug bounty program.

## Scope

In scope:

- the Go service in this repository, including its HTTP surface, configuration handling, and container image;
- the GitHub Actions workflows and any supply-chain weakness they introduce;
- documentation that describes a security control incorrectly in a way that would cause an insecure deployment.

Out of scope:

- vulnerabilities in third-party dependencies that already have a public advisory and an available upgrade — open a normal issue or a pull request instead;
- findings against deployments operated by someone else, which must be reported to that operator;
- results from automated scanners without a demonstrated impact on Convia;
- attacks that require a compromised host, a malicious operator, or physical access;
- denial of service by unbounded traffic volume against a self-hosted instance.

## Testing Guidelines

Test only against an instance you run yourself. Do not test against another person's deployment, do not attempt to access data that is not yours, and do not run automated scanning against infrastructure you do not own.

Reports that follow this policy are treated as good-faith research. The maintainers will not pursue action against a reporter who follows it, who acts only against their own instance, and who gives a reasonable window to publish a fix before disclosing publicly.

## Security Practices in This Repository

- Every push and pull request runs vulnerability analysis with `govulncheck` and a weekly scheduled scan.
- CodeQL analysis with extended security queries is available and runs when code scanning is enabled for the repository.
- GitHub Actions are pinned to full commit SHAs, workflows declare minimal `GITHUB_TOKEN` permissions, and checkout credentials are not persisted.
- Dependabot proposes weekly updates for GitHub Actions and Go modules.
- Secrets, credentials, and signing keys must never be committed. Configuration comes from the environment, as described in `AGENTS.md`.
