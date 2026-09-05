## Summary

<!-- What changes, and why. Reference the roadmap task ID when one applies, for example M02-005. -->

## Roadmap

<!-- Which TODO.md items this completes or advances, or "none" for maintenance work. -->

- Task IDs:

## Tests

- [ ] New behavior is covered by tests, or a bug fix includes a regression test
- [ ] `gofmt -l .`, `go vet ./...`, `go test ./...`, and `go build ./...` pass locally
- [ ] Tests are deterministic and do not depend on sleeps or external network access

## Security

- [ ] No credential, key, token, or secret is committed
- [ ] No secret, token, or personal data is written to logs or telemetry
- [ ] Untrusted input is validated, bounded, and never echoed into logs or responses unsafely
- [ ] Authorization is enforced at the service boundary, not only in the HTTP handler

## Public API

- [ ] No public contract exposes LiveKit, database, or other infrastructure concepts
- [ ] New endpoints follow `docs/api-conventions.md`, including the shared error schema
- [ ] `api/openapi.yaml` describes the change, and the contract test passes
- [ ] Compatibility follows `docs/api-compatibility.md`
- [ ] Changes to existing endpoints are additive, or the breaking change is called out below
- [ ] Not applicable

## Migrations

- [ ] No schema change
- [ ] Migration is reversible, or the forward-recovery plan is described below
- [ ] Migration is safe to run while the previous version is still serving traffic

## Documentation

- [ ] `TODO.md` is updated for every item this completes or advances
- [ ] `README.md`, `AGENTS.md`, or `docs/` is updated when documented behavior or architecture changed
- [ ] All new documentation and identifiers are in English

## Notes for Reviewers

<!-- Trade-offs, deferred work, known limitations, or anything a reviewer should look at first. -->
