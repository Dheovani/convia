# Convia Development Roadmap

This document is the operational development plan for Convia. It tracks what exists, what should be built next, why each milestone exists, and the conditions required to call a milestone complete.

## How to Use This File

- `[x]` means the item is implemented and has local evidence.
- `[ ]` means the item is not complete, even if design discussion has started.
- Keep task IDs stable so pull requests and issues can reference them.
- Work on the earliest incomplete milestone unless a production incident or explicit product decision changes priority.
- Do not create packages, infrastructure, or abstractions solely because they appear in this roadmap.
- Update the relevant checklist in the same pull request that implements an item.
- Add links to architecture decisions, API specifications, dashboards, or runbooks when they are created.
- A milestone is complete only when every exit criterion is satisfied.

## Priority Definitions

- **P0:** Required before the next architectural layer can be built safely.
- **P1:** Required for the first usable communication product.
- **P2:** Required for production readiness and external adoption.
- **P3:** Expansion work after the core platform is stable.

## Current Status

- **Current milestone:** M01 — Repository Governance and Hosted CI
- **Next implementation milestone:** M02 — HTTP and API Baseline
- **Current backend capability:** process startup, environment configuration, graceful shutdown, and `GET /health`
- **Current persistence capability:** none
- **Current authentication capability:** none
- **Current communication capability:** none
- **Current media capability:** none
- **Current user interface capability:** none

## Recommended Next Actions

Complete these in order before starting feature development:

1. [ ] **NEXT-001:** Initialize Git in this directory if it will be the repository root.
2. [ ] **NEXT-002:** Create or select the canonical GitHub repository without changing the Go module path until the repository path is known.
3. [ ] **NEXT-003:** Push the current foundation to a non-protected branch.
4. [ ] **NEXT-004:** Confirm that the `CI`, `Security`, and `Container` workflows all start on GitHub.
5. [ ] **NEXT-005:** Resolve any hosted-runner incompatibility, especially Go 1.26.5 or CodeQL availability.
6. [ ] **NEXT-006:** Enable GitHub code scanning where the repository plan and visibility support it.
7. [ ] **NEXT-007:** Configure branch protection to require successful CI, security, and container checks.
8. [ ] **NEXT-008:** Enable Dependabot alerts and review the first scheduled update run.
9. [ ] **NEXT-009:** Decide whether pull requests require one approving review before merge.
10. [ ] **NEXT-010:** Begin M02 only after the default branch has a green hosted build.

---

## Phase 0 — Engineering Foundation

### M00 — Backend Foundation

**Priority:** P0  
**Status:** Complete locally  
**Depends on:** Nothing  
**Goal:** Establish the smallest executable and testable Go service foundation.

- [x] **M00-001:** Preserve the repository engineering instructions in `AGENTS.md`.
- [x] **M00-002:** Use the canonical local module name `convia` without inventing a repository URL.
- [x] **M00-003:** Create the executable composition root at `cmd/convia/main.go`.
- [x] **M00-004:** Separate environment configuration into `internal/config`.
- [x] **M00-005:** Separate HTTP server construction into `internal/server`.
- [x] **M00-006:** Provide `GET /health` with a stable JSON response.
- [x] **M00-007:** Handle `SIGINT` and `SIGTERM` through a cancelable context.
- [x] **M00-008:** Shut down HTTP requests with a bounded timeout.
- [x] **M00-009:** Configure HTTP host and port through environment variables.
- [x] **M00-010:** Test configuration defaults, overrides, and invalid values.
- [x] **M00-011:** Test the health endpoint and unsupported HTTP methods.
- [x] **M00-012:** Add a non-root multi-stage container image.
- [x] **M00-013:** Document local run, test, build, and container commands.
- [x] **M00-014:** Pass `go fmt ./...`, `go vet ./...`, `go test ./...`, and `go build ./...` locally.

**Exit criteria:** The service starts without external infrastructure, reports health, stops gracefully, and passes the required local Go checks.

### M01 — Repository Governance and Hosted CI

**Priority:** P0  
**Status:** In progress  
**Depends on:** M00  
**Goal:** Make every proposed change pass repeatable quality, security, and container checks before merge.

- [x] **M01-001:** Add a CI workflow for formatting, `go vet`, tests, coverage, and build.
- [x] **M01-002:** Run the test suite with Go's race detector in CI.
- [x] **M01-003:** Run Staticcheck with a pinned tool version.
- [x] **M01-004:** Upload the coverage profile as a short-lived workflow artifact.
- [x] **M01-005:** Add a security workflow using `govulncheck`.
- [x] **M01-006:** Add CodeQL analysis with extended security queries.
- [x] **M01-007:** Add a scheduled weekly security scan.
- [x] **M01-008:** Add a container build workflow.
- [x] **M01-009:** Verify the configured runtime container user is non-root.
- [x] **M01-010:** Smoke test the containerized health endpoint.
- [x] **M01-011:** Give workflows minimal explicit `GITHUB_TOKEN` permissions.
- [x] **M01-012:** Pin third-party actions to full commit SHAs.
- [x] **M01-013:** Disable persisted checkout credentials.
- [x] **M01-014:** Add concurrency cancellation for superseded workflow runs.
- [x] **M01-015:** Add job timeouts to prevent stuck runners.
- [x] **M01-016:** Configure weekly Dependabot updates for GitHub Actions.
- [x] **M01-017:** Configure weekly Dependabot updates for Go modules.
- [ ] **M01-018:** Initialize the Git repository and publish it to GitHub.
- [ ] **M01-019:** Confirm all workflows pass on the first GitHub push.
- [ ] **M01-020:** Confirm workflows run correctly for pull requests from branches and forks.
- [ ] **M01-021:** Enable Dependabot alerts and security updates in repository settings.
- [ ] **M01-022:** Enable code scanning and confirm CodeQL results reach the Security tab.
- [ ] **M01-023:** Configure default-branch protection with required status checks.
- [ ] **M01-024:** Require branches to be current before merge.
- [ ] **M01-025:** Decide whether signed commits or signed tags are required.
- [ ] **M01-026:** Add a pull request template with test, security, API, migration, and documentation checkboxes.
- [ ] **M01-027:** Add issue templates for bugs, features, and security-safe reports.
- [ ] **M01-028:** Add `SECURITY.md` with private vulnerability reporting instructions.
- [ ] **M01-029:** Add `CONTRIBUTING.md` when contributors beyond the initial maintainers exist.
- [ ] **M01-030:** Record the names of required checks in repository documentation.
- [x] **M01-031:** Validate GitHub Actions workflow files with a pinned Actionlint version in CI.

**Exit criteria:** The default branch is protected, all hosted checks pass, dependency updates are automated, and repository security reporting is configured.

---

## Phase 1 — Public Control-Plane Foundations

### M02 — HTTP and API Baseline

**Priority:** P0  
**Status:** Not started  
**Depends on:** M01  
**Goal:** Define consistent HTTP behavior before adding public domain endpoints.

- [ ] **M02-001:** Decide the initial public API prefix, expected to be `/v1` unless an ADR selects another scheme.
- [ ] **M02-002:** Keep operational endpoints such as health outside public API versioning.
- [ ] **M02-003:** Define the standard JSON success envelope policy, including whether simple resources are returned directly.
- [ ] **M02-004:** Define a Convia-owned JSON error schema with stable machine-readable codes.
- [ ] **M02-005:** Add a request ID middleware that accepts or generates safe correlation IDs.
- [ ] **M02-006:** Return the request ID in response headers and structured logs.
- [ ] **M02-007:** Add panic recovery that logs internal context without exposing stack traces to clients.
- [ ] **M02-008:** Add bounded request body handling.
- [ ] **M02-009:** Reject unsupported content types on endpoints that accept JSON.
- [ ] **M02-010:** Reject malformed JSON and unknown fields consistently.
- [ ] **M02-011:** Define pagination fields and upper bounds before the first list endpoint.
- [ ] **M02-012:** Define timestamp serialization as UTC RFC 3339 with documented precision.
- [ ] **M02-013:** Define public identifier formatting without leaking future database internals.
- [ ] **M02-014:** Add server-level read, write, idle, and header timeout tests where behavior is non-trivial.
- [ ] **M02-015:** Add route-not-found and method-not-allowed JSON responses if public API consistency requires them.
- [ ] **M02-016:** Add tests for every middleware success and failure path.
- [ ] **M02-017:** Document reverse-proxy assumptions and trusted forwarded-header behavior.
- [ ] **M02-018:** Decide CORS behavior only after the standalone UI origin model is known.
- [ ] **M02-019:** Ensure operational endpoints cannot accidentally inherit public authentication requirements.
- [ ] **M02-020:** Document an endpoint implementation checklist for later milestones.

**Exit criteria:** New endpoints can follow one documented transport contract for errors, IDs, JSON, limits, and logging.

### M03 — API Specification and Compatibility Policy

**Priority:** P0  
**Status:** Not started  
**Depends on:** M02  
**Goal:** Treat Convia's public API as a stable product interface from its first domain endpoint.

- [ ] **M03-001:** Select OpenAPI as the REST contract format or record a justified alternative.
- [ ] **M03-002:** Add the initial API specification containing shared schemas and errors.
- [ ] **M03-003:** Document naming conventions for resources, fields, actions, and enums.
- [ ] **M03-004:** Document additive versus breaking API changes.
- [ ] **M03-005:** Define deprecation headers and minimum deprecation periods.
- [ ] **M03-006:** Define idempotency expectations for mutation endpoints.
- [ ] **M03-007:** Define optimistic concurrency behavior where updates can conflict.
- [ ] **M03-008:** Define pagination cursor opacity and stability requirements.
- [ ] **M03-009:** Add OpenAPI syntax validation to CI.
- [ ] **M03-010:** Add a breaking-change detector once a released baseline exists.
- [ ] **M03-011:** Add contract tests that compare implemented routes with the specification.
- [ ] **M03-012:** Document API lifecycle states: experimental, preview, stable, deprecated, removed.
- [ ] **M03-013:** Define public error code ownership and review requirements.
- [ ] **M03-014:** Define how SDK generation or handwritten SDKs consume the contract.
- [ ] **M03-015:** Record that media-provider-specific fields are forbidden in public contracts.

**Exit criteria:** The API contract is machine-readable, CI-validated, versioned, and governed by an explicit compatibility policy.

### M04 — PostgreSQL Foundation

**Priority:** P0  
**Status:** Not started  
**Depends on:** M03 and the first persistence-requiring domain decision  
**Goal:** Introduce PostgreSQL only when a real durable resource is ready to be implemented.

- [ ] **M04-001:** Select a PostgreSQL driver based on current Go support and operational requirements.
- [ ] **M04-002:** Select a migration tool with reversible and CI-friendly behavior.
- [ ] **M04-003:** Add database URL configuration with no committed credentials.
- [ ] **M04-004:** Add connection pool configuration with safe development defaults.
- [ ] **M04-005:** Validate mandatory production database settings at startup.
- [ ] **M04-006:** Establish migration filename and ordering conventions.
- [ ] **M04-007:** Add a local PostgreSQL service through Docker Compose.
- [ ] **M04-008:** Add an integration-test database isolated from developer data.
- [ ] **M04-009:** Add migration-up verification in CI.
- [ ] **M04-010:** Add migration-down or forward-recovery tests according to the chosen policy.
- [ ] **M04-011:** Add readiness behavior that distinguishes process health from database availability.
- [ ] **M04-012:** Add transaction helpers only after multiple operations need shared transaction ownership.
- [ ] **M04-013:** Define SQL query timeouts and context cancellation behavior.
- [ ] **M04-014:** Instrument pool saturation and query latency when observability is introduced.
- [ ] **M04-015:** Document backup, restore, and point-in-time recovery expectations before production.

**Exit criteria:** Migrations and integration tests run repeatably, connection failures are explicit, and no domain schema leaks through the public API.

### M05 — Applications and Tenancy

**Priority:** P0  
**Status:** Not started  
**Depends on:** M04  
**Goal:** Represent standalone Convia and external consumers as isolated Convia-owned applications.

- [ ] **M05-001:** Define the `Application` domain concept and invariants.
- [ ] **M05-002:** Decide whether standalone Convia is represented by a first-party application record.
- [ ] **M05-003:** Define application lifecycle states.
- [ ] **M05-004:** Define immutable public application IDs.
- [ ] **M05-005:** Add the applications database migration.
- [ ] **M05-006:** Add repository behavior for create, get, list, update, and lifecycle transitions.
- [ ] **M05-007:** Add an application service enforcing invariants independently of HTTP.
- [ ] **M05-008:** Add administrative HTTP endpoints only for immediately required operations.
- [ ] **M05-009:** Ensure every application-scoped query includes tenant isolation.
- [ ] **M05-010:** Test cross-application access denial.
- [ ] **M05-011:** Define application display metadata separately from security credentials.
- [ ] **M05-012:** Define safe deletion, suspension, and retention semantics.
- [ ] **M05-013:** Add audit events for security-relevant application changes.
- [ ] **M05-014:** Document bootstrap of the first administrative application.
- [ ] **M05-015:** Add API contract coverage for application responses and errors.

**Exit criteria:** Applications are durable, isolated tenants with tested lifecycle rules and no media-provider coupling.

### M06 — External Users and Identity Mapping

**Priority:** P0  
**Status:** Not started  
**Depends on:** M05  
**Goal:** Let each consuming application map its own users to stable Convia identities without sharing identity namespaces.

- [ ] **M06-001:** Define Convia's internal user identity and application-scoped external subject.
- [ ] **M06-002:** Decide whether one human can be linked across applications and document privacy implications.
- [ ] **M06-003:** Define uniqueness rules for `(application_id, external_subject)`.
- [ ] **M06-004:** Define display-name, avatar, and metadata ownership.
- [ ] **M06-005:** Set strict size and shape limits for application-provided metadata.
- [ ] **M06-006:** Add user and identity-mapping migrations.
- [ ] **M06-007:** Add create-or-resolve behavior with idempotent semantics.
- [ ] **M06-008:** Add suspension and deletion behavior.
- [ ] **M06-009:** Prevent cross-application identity enumeration.
- [ ] **M06-010:** Add tests for duplicate mappings and concurrent creation.
- [ ] **M06-011:** Define data export and erasure boundaries.
- [ ] **M06-012:** Define how standalone-product accounts map to the same domain model.
- [ ] **M06-013:** Add audit events for identity lifecycle changes.
- [ ] **M06-014:** Document which user attributes are authoritative in Convia.
- [ ] **M06-015:** Add public API examples using only Convia-owned concepts.

**Exit criteria:** Applications can safely resolve their users without identity collisions, enumeration, or provider-specific data.

### M07 — Authentication, Credentials, and Authorization

**Priority:** P0  
**Status:** Not started  
**Depends on:** M05 and M06  
**Goal:** Authenticate applications and users with explicit, least-privilege permissions.

- [ ] **M07-001:** Write a threat model for application credentials, user sessions, and media grants.
- [ ] **M07-002:** Decide the first supported server-to-server authentication mechanism.
- [ ] **M07-003:** Define credential identifiers separately from secret material.
- [ ] **M07-004:** Store only securely hashed or otherwise appropriately protected credentials.
- [ ] **M07-005:** Define credential creation, display-once, rotation, expiration, and revocation.
- [ ] **M07-006:** Define scopes using Convia domain operations.
- [ ] **M07-007:** Add authentication middleware after credential verification exists.
- [ ] **M07-008:** Add authorization at service boundaries, not only HTTP handlers.
- [ ] **M07-009:** Define first-party standalone UI session behavior separately from external API credentials.
- [ ] **M07-010:** Add replay resistance where signed requests or tokens require it.
- [ ] **M07-011:** Add rate limits for authentication failures.
- [ ] **M07-012:** Avoid logging raw credentials, bearer tokens, or signed media grants.
- [ ] **M07-013:** Add positive and negative tests for every scope.
- [ ] **M07-014:** Add cross-tenant authorization regression tests.
- [ ] **M07-015:** Add audit events for credential and permission changes.
- [ ] **M07-016:** Document emergency credential revocation procedures.
- [ ] **M07-017:** Define clock-skew tolerance for expiring tokens.
- [ ] **M07-018:** Add key rotation tests before introducing signed tokens.

**Exit criteria:** Every protected operation has an authenticated principal, explicit scope checks, safe credential lifecycle, and denial-path tests.

---

## Phase 2 — Communication Control Plane

### M08 — Room Domain

**Priority:** P1  
**Status:** Not started  
**Depends on:** M07  
**Goal:** Introduce Convia-owned rooms without any LiveKit terminology in public or domain contracts.

- [ ] **M08-001:** Define room purpose, lifecycle, visibility, and capacity invariants.
- [ ] **M08-002:** Decide which room properties are mutable after creation.
- [ ] **M08-003:** Define durable rooms versus ephemeral sessions.
- [ ] **M08-004:** Define application-scoped room aliases and uniqueness.
- [ ] **M08-005:** Add room persistence and indexes.
- [ ] **M08-006:** Add room create, read, list, update, close, and archive services.
- [ ] **M08-007:** Add idempotency for room creation.
- [ ] **M08-008:** Add public REST endpoints and OpenAPI schemas.
- [ ] **M08-009:** Add pagination and filtering tests.
- [ ] **M08-010:** Add room membership or access-policy concepts only when required.
- [ ] **M08-011:** Prevent application A from resolving application B's room.
- [ ] **M08-012:** Define room closure behavior for active calls.
- [ ] **M08-013:** Define retention and deletion behavior.
- [ ] **M08-014:** Add domain, repository, HTTP, and concurrency tests.
- [ ] **M08-015:** Add room audit events.

**Exit criteria:** Applications can manage isolated rooms through stable Convia APIs with complete lifecycle and authorization tests.

### M09 — Call Lifecycle

**Priority:** P1  
**Status:** Not started  
**Depends on:** M08  
**Goal:** Model calls as Convia control-plane resources independently from media infrastructure.

- [ ] **M09-001:** Define call states and allowed transitions.
- [ ] **M09-002:** Define whether a room can have multiple historical calls and one active call.
- [ ] **M09-003:** Define call initiation, ringing, active, ending, ended, and failed semantics as needed.
- [ ] **M09-004:** Define actor and reason fields for transitions.
- [ ] **M09-005:** Add durable call records and transition history.
- [ ] **M09-006:** Enforce one-active-call constraints transactionally where required.
- [ ] **M09-007:** Add idempotent start and end operations.
- [ ] **M09-008:** Add call REST endpoints and contract schemas.
- [ ] **M09-009:** Reject invalid transitions with stable public errors.
- [ ] **M09-010:** Define behavior when the media provider is temporarily unavailable.
- [ ] **M09-011:** Keep provider session IDs internal.
- [ ] **M09-012:** Add concurrent transition tests.
- [ ] **M09-013:** Add call history filtering and pagination.
- [ ] **M09-014:** Add call audit events and timestamps.
- [ ] **M09-015:** Define reconciliation behavior for stale active calls.

**Exit criteria:** Calls have a durable, concurrency-safe Convia lifecycle that remains meaningful without a media provider.

### M10 — Participants and Invitations

**Priority:** P1  
**Status:** Not started  
**Depends on:** M09  
**Goal:** Represent who may join and who actually participates in a call.

- [ ] **M10-001:** Distinguish room membership, invitation, and call participation.
- [ ] **M10-002:** Define participant lifecycle states.
- [ ] **M10-003:** Define participant roles and capabilities using Convia concepts.
- [ ] **M10-004:** Define invite creation, acceptance, rejection, expiration, and revocation.
- [ ] **M10-005:** Add participant and invitation persistence.
- [ ] **M10-006:** Enforce call capacity and uniqueness rules.
- [ ] **M10-007:** Add join authorization independent of media token issuance.
- [ ] **M10-008:** Add leave and forced-removal behavior.
- [ ] **M10-009:** Define reconnection behavior without creating duplicate participants.
- [ ] **M10-010:** Define guest participation requirements.
- [ ] **M10-011:** Add REST endpoints and OpenAPI schemas.
- [ ] **M10-012:** Add concurrent join and capacity tests.
- [ ] **M10-013:** Add authorization tests for moderator actions.
- [ ] **M10-014:** Add participant audit and call-history events.
- [ ] **M10-015:** Define privacy rules for participant lists.

**Exit criteria:** Participation is authorized, capacity-safe, reconnectable, auditable, and independent from LiveKit identities.

### M11 — Internal Media Boundary

**Priority:** P1  
**Status:** Not started  
**Depends on:** M09 and M10  
**Goal:** Create the smallest internal abstraction that prevents LiveKit from leaking into the domain or public API.

- [ ] **M11-001:** Enumerate the exact media operations required by implemented call flows.
- [ ] **M11-002:** Define the interface in the consuming internal package.
- [ ] **M11-003:** Use Convia-owned request and response types at the boundary.
- [ ] **M11-004:** Keep provider room names, grants, tokens, and metadata internal.
- [ ] **M11-005:** Define error translation from provider failures to Convia service errors.
- [ ] **M11-006:** Define retryable versus terminal provider failures.
- [ ] **M11-007:** Define idempotency expectations for provider operations.
- [ ] **M11-008:** Add a deterministic fake for service-level tests only if the interface is justified.
- [ ] **M11-009:** Test that provider failures do not corrupt call state.
- [ ] **M11-010:** Record an ADR describing the control-plane/media-plane boundary.
- [ ] **M11-011:** Add an architecture test or review check preventing provider imports outside adapters.
- [ ] **M11-012:** Avoid designing abstractions for unsupported providers.

**Exit criteria:** Implemented call flows depend on a narrow internal media capability, and no public contract contains LiveKit-specific concepts.

### M12 — LiveKit Adapter

**Priority:** P1  
**Status:** Not started  
**Depends on:** M11  
**Goal:** Implement the initial media plane behind the internal boundary.

- [ ] **M12-001:** Pin a supported LiveKit server and Go SDK version.
- [ ] **M12-002:** Add LiveKit endpoint, key, and secret configuration with strict validation.
- [ ] **M12-003:** Ensure secrets are never logged or returned through APIs.
- [ ] **M12-004:** Map Convia calls to internal provider rooms.
- [ ] **M12-005:** Map Convia participant capabilities to least-privilege provider grants.
- [ ] **M12-006:** Issue short-lived media connection credentials.
- [ ] **M12-007:** Implement provider room creation only if eager creation is required.
- [ ] **M12-008:** Implement participant removal and room termination operations.
- [ ] **M12-009:** Verify provider webhook signatures before trusting events.
- [ ] **M12-010:** Translate provider events into Convia-owned internal events.
- [ ] **M12-011:** Add adapter unit tests around mapping and error translation.
- [ ] **M12-012:** Add integration tests against an isolated LiveKit container.
- [ ] **M12-013:** Add timeout, retry, and circuit-breaking behavior based on measured failure modes.
- [ ] **M12-014:** Add reconciliation for control-plane/provider divergence.
- [ ] **M12-015:** Document local LiveKit setup without exposing it to external consumers.

**Exit criteria:** Authorized Convia participants can obtain media access while all LiveKit details remain internal and integration-tested.

### M13 — Client Session Bootstrap

**Priority:** P1  
**Status:** Not started  
**Depends on:** M10 and M12  
**Goal:** Provide clients one Convia endpoint for joining a call and receiving short-lived connection instructions.

- [ ] **M13-001:** Define the public join-session response without provider-specific field names.
- [ ] **M13-002:** Include only the connection data required by supported clients.
- [ ] **M13-003:** Bind media grants to application, user, call, participant, and capabilities.
- [ ] **M13-004:** Set short expiration and document renewal behavior.
- [ ] **M13-005:** Prevent reuse after participant removal or call termination where technically possible.
- [ ] **M13-006:** Add idempotent join-session creation.
- [ ] **M13-007:** Add authorization, capacity, suspended-user, and ended-call denial tests.
- [ ] **M13-008:** Add rate limits for token issuance.
- [ ] **M13-009:** Ensure tokens and connection secrets are redacted from telemetry.
- [ ] **M13-010:** Document the WebRTC connection sequence for SDK authors.
- [ ] **M13-011:** Add contract examples for successful join and stable failures.
- [ ] **M13-012:** Add an end-to-end join test using disposable infrastructure.

**Exit criteria:** A client can join through Convia alone; external consumers never need direct server-side LiveKit integration.

### M14 — Real-Time Control Events

**Priority:** P1  
**Status:** Not started  
**Depends on:** M09 and M10  
**Goal:** Use WebSocket only for control-plane events that materially require low-latency delivery.

- [ ] **M14-001:** Enumerate events that cannot be handled adequately through REST polling or webhooks.
- [ ] **M14-002:** Define a versioned Convia event envelope.
- [ ] **M14-003:** Define event IDs, timestamps, subject types, and correlation IDs.
- [ ] **M14-004:** Define connection authentication and authorization.
- [ ] **M14-005:** Define subscription scopes and tenant isolation.
- [ ] **M14-006:** Define reconnect, resume cursor, and missed-event behavior.
- [ ] **M14-007:** Define heartbeat and idle timeout behavior.
- [ ] **M14-008:** Apply bounded queues and explicit slow-consumer handling.
- [ ] **M14-009:** Apply maximum message and connection limits.
- [ ] **M14-010:** Prevent WebSocket use for ordinary audio or video payloads.
- [ ] **M14-011:** Add protocol contract tests.
- [ ] **M14-012:** Add concurrent connect, disconnect, and shutdown tests.
- [ ] **M14-013:** Add metrics for active connections, delivery latency, and dropped events.
- [ ] **M14-014:** Document horizontal scaling requirements before adding Redis pub/sub.

**Exit criteria:** Authorized clients receive bounded, versioned, resumable control events without carrying media payloads.

### M15 — Webhooks for External Applications

**Priority:** P1  
**Status:** Not started  
**Depends on:** Stable domain events from M09 and M10  
**Goal:** Notify server-side consumers of durable Convia events reliably and securely.

- [ ] **M15-001:** Define webhook endpoint registration and lifecycle.
- [ ] **M15-002:** Define event subscriptions per endpoint.
- [ ] **M15-003:** Define a versioned webhook envelope shared with domain event semantics where appropriate.
- [ ] **M15-004:** Sign deliveries using rotating application-specific secrets.
- [ ] **M15-005:** Include delivery IDs, timestamps, and replay-defense guidance.
- [ ] **M15-006:** Persist delivery attempts durably.
- [ ] **M15-007:** Define retry schedule, maximum age, and terminal failure behavior.
- [ ] **M15-008:** Add idempotency guidance for consumers.
- [ ] **M15-009:** Add endpoint disablement after sustained failures.
- [ ] **M15-010:** Add manual redelivery with authorization and audit logging.
- [ ] **M15-011:** Protect against SSRF and unsafe destination networks.
- [ ] **M15-012:** Apply connection, response-size, redirect, and timeout limits.
- [ ] **M15-013:** Redact secrets and sensitive payloads from logs.
- [ ] **M15-014:** Add deterministic retry and signature tests.
- [ ] **M15-015:** Add integration tests with a local webhook receiver.

**Exit criteria:** External applications receive signed, retryable, auditable events without relying on internal provider webhooks.

### M16 — Redis and Distributed Ephemeral State

**Priority:** P1  
**Status:** Not started  
**Depends on:** A demonstrated distributed-state requirement from M14, M15, or M17  
**Goal:** Introduce Redis for justified ephemeral coordination, never as the durable source of truth.

- [ ] **M16-001:** Document the first concrete Redis use case before adding the dependency.
- [ ] **M16-002:** Select and pin a maintained Redis client.
- [ ] **M16-003:** Add connection and pool configuration.
- [ ] **M16-004:** Add local Redis through Docker Compose.
- [ ] **M16-005:** Define key naming, tenant scoping, and versioning conventions.
- [ ] **M16-006:** Define TTL for every ephemeral key category.
- [ ] **M16-007:** Prevent secrets and unnecessary personal data from entering Redis.
- [ ] **M16-008:** Define behavior when Redis is unavailable.
- [ ] **M16-009:** Add integration tests against disposable Redis.
- [ ] **M16-010:** Add metrics for pool usage, operation latency, and failures.
- [ ] **M16-011:** Document eviction-policy assumptions.
- [ ] **M16-012:** Test that durable state remains recoverable without Redis.

**Exit criteria:** Redis supports a documented ephemeral need, has bounded data lifetimes, and cannot become an accidental durable authority.

### M17 — Presence

**Priority:** P1  
**Status:** Not started  
**Depends on:** M14 and M16  
**Goal:** Expose useful, privacy-aware presence derived from ephemeral signals.

- [ ] **M17-001:** Define presence states and their exact semantics.
- [ ] **M17-002:** Distinguish application presence, Convia connection presence, and call participation.
- [ ] **M17-003:** Define heartbeat, timeout, and disconnect transitions.
- [ ] **M17-004:** Define multi-device aggregation behavior.
- [ ] **M17-005:** Define visibility and privacy policies per application.
- [ ] **M17-006:** Store presence only as ephemeral state.
- [ ] **M17-007:** Publish Convia-owned presence events.
- [ ] **M17-008:** Handle unclean disconnects and process crashes.
- [ ] **M17-009:** Test clock skew and delayed heartbeat behavior.
- [ ] **M17-010:** Test cross-node presence convergence.
- [ ] **M17-011:** Add metrics for active users and stale entries without high-cardinality labels.
- [ ] **M17-012:** Document that presence is advisory rather than a durable guarantee.

**Exit criteria:** Presence converges across instances, respects privacy, expires safely, and is not confused with durable participation history.

---

## Phase 3 — Product and Integration Surfaces

### M18 — Standalone Web Application

**Priority:** P1  
**Status:** Not started  
**Depends on:** M07, M08, M09, M10, and M13  
**Goal:** Deliver Convia's own user interface on top of the same public platform concepts offered to external consumers.

- [ ] **M18-001:** Choose the frontend stack based on team capability and long-term maintenance.
- [ ] **M18-002:** Define first-party authentication and session management.
- [ ] **M18-003:** Build a room list and room creation flow.
- [ ] **M18-004:** Build call start, join, leave, and end flows.
- [ ] **M18-005:** Add microphone and camera permission handling.
- [ ] **M18-006:** Add device selection and persisted preferences.
- [ ] **M18-007:** Add pre-join media preview.
- [ ] **M18-008:** Add participant roster and call-state feedback.
- [ ] **M18-009:** Add responsive layouts for supported screen sizes.
- [ ] **M18-010:** Meet keyboard navigation and screen-reader requirements.
- [ ] **M18-011:** Handle denied permissions and unavailable devices clearly.
- [ ] **M18-012:** Handle reconnection and degraded network states.
- [ ] **M18-013:** Add component tests for user-visible state transitions.
- [ ] **M18-014:** Add a small set of end-to-end tests for critical call journeys.
- [ ] **M18-015:** Ensure the UI uses Convia APIs rather than privileged internal shortcuts.

**Exit criteria:** A user can complete the supported room and call journey accessibly through Convia's standalone product.

### M19 — TypeScript Client SDK

**Priority:** P1  
**Status:** Not started  
**Depends on:** Stable M03 and M13 contracts  
**Goal:** Let browser applications integrate with Convia without directly implementing its HTTP and event protocols.

- [ ] **M19-001:** Define supported browser and TypeScript versions.
- [ ] **M19-002:** Decide generated versus handwritten REST client boundaries.
- [ ] **M19-003:** Expose Convia-owned types and errors.
- [ ] **M19-004:** Implement authenticated REST transport with cancellation.
- [ ] **M19-005:** Implement idempotency-key support for mutations.
- [ ] **M19-006:** Implement real-time control-event connection and reconnection.
- [ ] **M19-007:** Wrap media connection details without requiring server-side LiveKit knowledge.
- [ ] **M19-008:** Define stable event listener and cleanup behavior.
- [ ] **M19-009:** Add unit tests for serialization and error translation.
- [ ] **M19-010:** Add browser integration tests against a disposable Convia stack.
- [ ] **M19-011:** Publish API reference and minimal integration examples.
- [ ] **M19-012:** Add package provenance, integrity, and release automation.
- [ ] **M19-013:** Define semantic versioning and deprecation policy.
- [ ] **M19-014:** Test compatibility with Orbit and Workspace Town prototypes.

**Exit criteria:** A supported browser application can authenticate, manage calls, receive events, and join media using only Convia's SDK and public contracts.

### M20 — Server SDKs and Integration Examples

**Priority:** P2  
**Status:** Not started  
**Depends on:** Stable M03, M07, M08, M09, and M15 contracts  
**Goal:** Make server-to-server integration safe, idiomatic, and well documented.

- [ ] **M20-001:** Prioritize SDK languages using confirmed consumer requirements.
- [ ] **M20-002:** Implement the first server SDK with explicit timeouts and contexts.
- [ ] **M20-003:** Implement credential handling without logging secrets.
- [ ] **M20-004:** Expose typed Convia errors and retry guidance.
- [ ] **M20-005:** Support idempotent mutation requests.
- [ ] **M20-006:** Add webhook signature verification helpers.
- [ ] **M20-007:** Add pagination iterators without hiding network errors.
- [ ] **M20-008:** Add unit tests against contract fixtures.
- [ ] **M20-009:** Add integration tests against a disposable service.
- [ ] **M20-010:** Publish runnable examples for room, call, participant, and webhook flows.
- [ ] **M20-011:** Add semantic versioning and compatibility documentation.
- [ ] **M20-012:** Add release provenance and checksum verification.

**Exit criteria:** At least one real external application integrates server-side without constructing raw requests or knowing media-provider details.

### M21 — Administration and Operations Surface

**Priority:** P2  
**Status:** Not started  
**Depends on:** M05 through M17 as applicable  
**Goal:** Give authorized operators safe visibility and control without direct database manipulation.

- [ ] **M21-001:** Define operator roles separately from tenant application roles.
- [ ] **M21-002:** Require strong authentication for operator access.
- [ ] **M21-003:** Add application lookup and lifecycle controls.
- [ ] **M21-004:** Add credential revocation and rotation controls.
- [ ] **M21-005:** Add room and call inspection using redacted data.
- [ ] **M21-006:** Add participant removal and emergency call termination.
- [ ] **M21-007:** Add webhook delivery inspection and redelivery.
- [ ] **M21-008:** Add audit-log search with strict access controls.
- [ ] **M21-009:** Require reasons for high-impact operator actions.
- [ ] **M21-010:** Add tests preventing privilege escalation.
- [ ] **M21-011:** Add confirmation and re-authentication for destructive actions.
- [ ] **M21-012:** Document operational ownership and escalation paths.

**Exit criteria:** Routine support and incident actions can be performed through audited, least-privilege operations rather than database access.

---

## Phase 4 — Production Readiness

### M22 — OpenTelemetry and Structured Observability

**Priority:** P2  
**Status:** Not started  
**Depends on:** Meaningful domain and infrastructure behavior  
**Goal:** Make failures and performance understandable across HTTP, database, Redis, webhooks, and media adapters.

- [ ] **M22-001:** Define service name, environment, version, and instance resource attributes.
- [ ] **M22-002:** Add OpenTelemetry configuration with disabled-by-default local behavior if appropriate.
- [ ] **M22-003:** Trace inbound HTTP requests with safe route names.
- [ ] **M22-004:** Propagate trace context to supported outbound calls.
- [ ] **M22-005:** Instrument database, Redis, webhook, and media adapter boundaries.
- [ ] **M22-006:** Define request, error, latency, and saturation metrics.
- [ ] **M22-007:** Define call-control metrics without user or room IDs as metric labels.
- [ ] **M22-008:** Correlate structured logs with trace and request IDs.
- [ ] **M22-009:** Redact credentials, tokens, personal data, and sensitive metadata.
- [ ] **M22-010:** Add telemetry tests with in-memory exporters.
- [ ] **M22-011:** Create initial service and dependency dashboards.
- [ ] **M22-012:** Define SLOs for availability, latency, and media-join control operations.
- [ ] **M22-013:** Create actionable alerts tied to runbooks.
- [ ] **M22-014:** Define telemetry retention and sampling policies.

**Exit criteria:** Operators can trace a failed request across dependencies, measure SLOs, and investigate without exposing sensitive data.

### M23 — Security and Privacy Hardening

**Priority:** P2  
**Status:** Not started  
**Depends on:** Threat models for implemented features  
**Goal:** Systematically reduce application, infrastructure, supply-chain, and privacy risk.

- [ ] **M23-001:** Maintain a living threat model for each trust boundary.
- [ ] **M23-002:** Classify stored and transmitted data by sensitivity.
- [ ] **M23-003:** Define encryption-in-transit requirements for every connection.
- [ ] **M23-004:** Define encryption-at-rest responsibilities and key ownership.
- [ ] **M23-005:** Move production secrets to an approved secret manager.
- [ ] **M23-006:** Define secret and signing-key rotation procedures.
- [ ] **M23-007:** Add secret scanning and push protection in GitHub.
- [ ] **M23-008:** Add dependency license policy and review automation if required.
- [ ] **M23-009:** Generate a software bill of materials for release images.
- [ ] **M23-010:** Sign release images and publish provenance attestations.
- [ ] **M23-011:** Scan built container images for known vulnerabilities.
- [ ] **M23-012:** Define patch deadlines by vulnerability severity.
- [ ] **M23-013:** Add abuse controls and tenant-aware rate limits.
- [ ] **M23-014:** Test SSRF, injection, broken access control, and token leakage risks.
- [ ] **M23-015:** Run an external penetration test before general availability.
- [ ] **M23-016:** Define vulnerability intake, triage, disclosure, and remediation procedures.
- [ ] **M23-017:** Define personal-data export, correction, deletion, and retention workflows.
- [ ] **M23-018:** Review Brazilian LGPD and other applicable regulatory obligations with qualified counsel.

**Exit criteria:** Documented controls cover the implemented attack surface, critical findings are resolved, and privacy operations are executable.

### M24 — Test Strategy and Reliability

**Priority:** P2  
**Status:** Not started  
**Depends on:** Each implemented domain milestone  
**Goal:** Build confidence through deterministic layers of tests and explicit failure-mode coverage.

- [ ] **M24-001:** Maintain unit tests for every domain invariant and transition.
- [ ] **M24-002:** Maintain HTTP contract tests for every endpoint and error class.
- [ ] **M24-003:** Maintain database integration tests against disposable PostgreSQL.
- [ ] **M24-004:** Maintain Redis integration tests only for implemented Redis behavior.
- [ ] **M24-005:** Maintain LiveKit adapter integration tests against a pinned server version.
- [ ] **M24-006:** Maintain webhook delivery tests with deterministic clocks and retry scheduling.
- [ ] **M24-007:** Maintain WebSocket connection and slow-consumer tests.
- [ ] **M24-008:** Add end-to-end tests only for critical standalone and SDK journeys.
- [ ] **M24-009:** Track coverage by meaningful package and critical behavior.
- [ ] **M24-010:** Set coverage gates only after a baseline and risk review.
- [ ] **M24-011:** Run race detection on every pull request.
- [ ] **M24-012:** Add fuzz tests for parsers, identifiers, webhook signatures, and state machines.
- [ ] **M24-013:** Add failure-injection tests for database, Redis, webhook, and media outages.
- [ ] **M24-014:** Quarantine no flaky test without an owner, issue, and removal deadline.
- [ ] **M24-015:** Publish test duration and flake trends.
- [ ] **M24-016:** Add backup restore and disaster-recovery exercises.

**Exit criteria:** Critical behavior is covered at the lowest reliable test layer, failure modes are exercised, and flaky tests are actively eliminated.

### M25 — Performance and Horizontal Scaling

**Priority:** P2  
**Status:** Not started  
**Depends on:** Stable critical flows and production-like observability  
**Goal:** Validate that the control plane scales without relying on premature optimization.

- [ ] **M25-001:** Define expected tenant, user, room, call, participant, and connection volumes.
- [ ] **M25-002:** Define latency and throughput targets for critical endpoints.
- [ ] **M25-003:** Create representative load-test scenarios.
- [ ] **M25-004:** Measure baseline CPU, memory, allocation, and goroutine behavior.
- [ ] **M25-005:** Profile database queries and verify indexes with realistic data sizes.
- [ ] **M25-006:** Test connection pool saturation and recovery.
- [ ] **M25-007:** Test WebSocket fan-out and slow consumers across multiple instances.
- [ ] **M25-008:** Test Redis failure and failover behavior where Redis is used.
- [ ] **M25-009:** Test media-provider control API degradation independently from media quality.
- [ ] **M25-010:** Verify graceful shutdown while calls and control connections are active.
- [ ] **M25-011:** Verify no in-memory state prevents horizontal scaling.
- [ ] **M25-012:** Establish performance regression thresholds in scheduled CI.
- [ ] **M25-013:** Document capacity assumptions and scaling triggers.
- [ ] **M25-014:** Optimize only measured bottlenecks with before-and-after evidence.

**Exit criteria:** Measured capacity meets documented targets, instances scale horizontally, and performance regressions are detectable.

### M26 — Deployment and Operations

**Priority:** P2  
**Status:** Not started  
**Depends on:** M22 through M25  
**Goal:** Deploy, operate, recover, and roll back Convia predictably.

- [ ] **M26-001:** Select the initial deployment environment based on actual operational requirements.
- [ ] **M26-002:** Define immutable image naming and promotion between environments.
- [ ] **M26-003:** Add environment-specific configuration validation.
- [ ] **M26-004:** Add liveness, readiness, and startup probes with distinct semantics.
- [ ] **M26-005:** Define rolling deployment and connection-draining behavior.
- [ ] **M26-006:** Define migration execution ownership and rollback policy.
- [ ] **M26-007:** Define database backup frequency, retention, encryption, and restore tests.
- [ ] **M26-008:** Define Redis persistence expectations based on its actual uses.
- [ ] **M26-009:** Define LiveKit deployment and capacity ownership.
- [ ] **M26-010:** Add staging with production-like topology and isolated data.
- [ ] **M26-011:** Add deployment smoke tests and automated rollback signals.
- [ ] **M26-012:** Write runbooks for common dependency and saturation incidents.
- [ ] **M26-013:** Define on-call ownership and incident severity levels.
- [ ] **M26-014:** Run a restore drill and a rollback drill before production launch.
- [ ] **M26-015:** Add Kubernetes only if the selected environment and scaling model justify it.

**Exit criteria:** A tested process exists for deployment, migration, rollback, incident response, backup, and restore.

### M27 — Release and Compatibility Management

**Priority:** P2  
**Status:** Not started  
**Depends on:** Stable API and deployment process  
**Goal:** Release the service, contracts, images, and SDKs as one governed platform.

- [ ] **M27-001:** Define semantic versioning boundaries for service, API, events, and SDKs.
- [ ] **M27-002:** Define release branch and tag policy.
- [ ] **M27-003:** Generate changelogs from reviewed change metadata.
- [ ] **M27-004:** Build release binaries and images from protected tags.
- [ ] **M27-005:** Publish checksums, signatures, SBOMs, and provenance.
- [ ] **M27-006:** Verify artifacts in a clean environment before publication.
- [ ] **M27-007:** Add compatibility tests for supported SDK and API versions.
- [ ] **M27-008:** Define database migration compatibility during rolling upgrades.
- [ ] **M27-009:** Define event-schema compatibility during mixed-version deployments.
- [ ] **M27-010:** Document upgrade and rollback instructions.
- [ ] **M27-011:** Publish deprecation notices through documented channels.
- [ ] **M27-012:** Maintain a supported-version matrix.

**Exit criteria:** Releases are reproducible, signed, documented, backward-compatible within policy, and safely reversible.

---

## Phase 5 — Advanced Communication Capabilities

### M28 — Screen Sharing

**Priority:** P3  
**Status:** Not started  
**Depends on:** Stable video calls and client SDK  
**Goal:** Add screen sharing as a Convia capability with explicit authorization and user feedback.

- [ ] **M28-001:** Define who may start screen sharing.
- [ ] **M28-002:** Define simultaneous-share limits.
- [ ] **M28-003:** Map the capability internally to provider permissions.
- [ ] **M28-004:** Add browser capture and cancellation handling.
- [ ] **M28-005:** Add clear active-share indicators.
- [ ] **M28-006:** Handle browser and operating-system support differences.
- [ ] **M28-007:** Add start, stop, replacement, and disconnect tests.
- [ ] **M28-008:** Add control events without exposing provider track concepts publicly.
- [ ] **M28-009:** Add quality and bandwidth telemetry.
- [ ] **M28-010:** Document security risks around accidental content sharing.

**Exit criteria:** Authorized users can reliably start and stop screen sharing with clear UI state and provider-independent contracts.

### M29 — Recording, Transcription, and Derived Media

**Priority:** P3  
**Status:** Not started  
**Depends on:** Legal, privacy, storage, and product approval  
**Goal:** Add derived-media features only with explicit consent, retention, and access controls.

- [ ] **M29-001:** Confirm product requirements and applicable consent laws.
- [ ] **M29-002:** Define explicit recording authorization and participant notification.
- [ ] **M29-003:** Define recording lifecycle and failure states.
- [ ] **M29-004:** Select secure object storage and encryption controls.
- [ ] **M29-005:** Define retention, deletion, legal hold, and export behavior.
- [ ] **M29-006:** Define transcript ownership and access controls.
- [ ] **M29-007:** Isolate provider recording identifiers internally.
- [ ] **M29-008:** Add audit events for every recording and transcript access.
- [ ] **M29-009:** Add malware and content-safety controls where files are processed.
- [ ] **M29-010:** Add end-to-end consent, failure, retention, and deletion tests.

**Exit criteria:** Derived media is consented, encrypted, access-controlled, auditable, and deletable according to documented policy.

### M30 — General Availability Readiness

**Priority:** P2  
**Status:** Not started  
**Depends on:** All P0, P1, and selected P2 milestones  
**Goal:** Verify that Convia is supportable as both a standalone product and an external platform.

- [ ] **M30-001:** Freeze and review the initial stable public API surface.
- [ ] **M30-002:** Complete an architecture review focused on tenant isolation and media abstraction.
- [ ] **M30-003:** Complete security and privacy reviews with no unresolved critical findings.
- [ ] **M30-004:** Complete load, soak, failover, backup, and restore tests.
- [ ] **M30-005:** Validate the standalone critical user journeys.
- [ ] **M30-006:** Validate at least one Orbit integration in a non-production environment.
- [ ] **M30-007:** Validate at least one Workspace Town integration in a non-production environment.
- [ ] **M30-008:** Publish API, SDK, webhook, authentication, and operational documentation.
- [ ] **M30-009:** Publish service limits, support boundaries, and status communication channels.
- [ ] **M30-010:** Establish production SLOs, dashboards, alerts, and on-call coverage.
- [ ] **M30-011:** Complete incident-response and disaster-recovery exercises.
- [ ] **M30-012:** Confirm release, rollback, credential rotation, and emergency revocation procedures.
- [ ] **M30-013:** Resolve or explicitly accept every launch-blocking risk.
- [ ] **M30-014:** Produce a launch checklist with named owners and dates.
- [ ] **M30-015:** Conduct a post-launch review and update this roadmap from real usage.

**Exit criteria:** Convia has stable contracts, verified integrations, operational ownership, tested recovery, and no unaccepted launch-blocking risk.

---

## Explicitly Deferred Until Required

The following items are intentionally not current implementation tasks:

- Kubernetes manifests before a deployment platform requires Kubernetes.
- Multiple backend microservices before independent scaling or ownership justifies them.
- A second media provider before a real portability requirement exists.
- Custom WebRTC, RTP, or SFU implementations.
- GraphQL, CQRS, event sourcing, or a message broker without a demonstrated need.
- PostgreSQL or Redis packages before the milestone that consumes them begins.
- SDK packages before stable public contracts exist.
- Recording or transcription before consent, privacy, retention, and storage decisions are approved.
- Coverage exclusions or vanity thresholds that conceal meaningful untested behavior.

## Roadmap Maintenance Checklist

- [ ] **ROADMAP-001:** Review this file at the start of every milestone.
- [ ] **ROADMAP-002:** Review priorities monthly or after a major product decision.
- [ ] **ROADMAP-003:** Split tasks into GitHub issues when work is scheduled, preserving task IDs.
- [ ] **ROADMAP-004:** Assign an owner and target release when a task becomes active.
- [ ] **ROADMAP-005:** Add new tasks when implementation reveals missing work.
- [ ] **ROADMAP-006:** Remove tasks only with a documented reason or superseding decision.
- [ ] **ROADMAP-007:** Keep completed items checked instead of deleting project history.
- [ ] **ROADMAP-008:** Keep the `Current Status` and `Recommended Next Actions` sections accurate.
