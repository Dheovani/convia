# AGENTS.md

## Project

**Convia** is a standalone real-time communication platform.

Its long-term purpose is to provide reusable communication capabilities including voice, video, screen sharing, rooms, presence, and related real-time features.

Convia is both:

- a standalone product with its own user-facing application; and
- a platform that other products can integrate with through stable APIs and SDKs.

Known future consumers include applications such as Orbit and Workspace Town, but Convia must never be architecturally coupled to any single consumer.

---

## Language

The backend is written in **Go**.

Use the Go version declared in `go.mod`.

Prefer idiomatic Go over patterns imported from Java, C#, C++, or other languages.

---

## Documentation Language

**All project documentation must be written in English.**

This includes:

- README files;
- Markdown documentation;
- architecture documents;
- API documentation;
- source-code documentation comments;
- examples intended for repository users;
- configuration documentation;
- developer instructions.

Identifiers must also use English names.

Do not introduce Portuguese documentation into the repository.

---

## Licensing

Convia is source-available under the PolyForm Noncommercial License 1.0.0. It is free for noncommercial use, and its commercial rights are reserved by the copyright holder.

This constrains dependencies. Every dependency must allow Convia to be distributed under those terms and to be relicensed commercially by the copyright holder.

Prefer permissively licensed dependencies such as BSD, MIT, and Apache-2.0.

Do not add a dependency under a copyleft license such as GPL, LGPL, or AGPL, and do not copy code from a copyleft project into this repository.

Do not copy code from any source without confirming its license permits this use.

---

## Core Architecture

Convia must distinguish between two major architectural areas.

### Control Plane

Convia owns the control plane.

Examples include:

- applications;
- users;
- authentication;
- authorization;
- rooms;
- calls;
- participants;
- presence;
- permissions;
- invitations;
- tokens;
- call state;
- call history;
- webhooks;
- external integrations;
- public APIs;
- SDK-facing contracts.

### Media Plane

The media plane is responsible for transporting real-time media.

Examples include:

- WebRTC;
- RTP/RTCP;
- ICE;
- STUN/TURN;
- audio;
- video;
- screen sharing;
- SFU behavior;
- media quality adaptation.

The initial media implementation is expected to use **LiveKit**.

However:

> LiveKit is an infrastructure implementation detail, not Convia's public domain model.

Public APIs, domain types, and external SDKs must not expose LiveKit-specific concepts unless there is an unavoidable protocol-level reason.

All interaction with LiveKit should eventually be isolated behind internal adapter boundaries.

This must make it possible to replace or supplement LiveKit in the future without redesigning Convia's public API.

---

## Planned Technology Direction

The intended platform stack is:

- Go
- PostgreSQL
- Redis
- LiveKit
- WebRTC
- REST
- WebSocket where appropriate
- OpenTelemetry
- Docker

These technologies are the current architectural direction, not permission to introduce all of them immediately.

Add infrastructure only when the current feature requires it.

---

## Engineering Principles

### Keep the Design Small

Do not build abstractions for hypothetical future requirements.

Prefer:

1. a concrete implementation;
2. a clear package boundary;
3. an interface only when multiple implementations, testing boundaries, or architectural isolation justify it.

Avoid speculative interfaces and empty packages.

### Prefer the Standard Library

Use the Go standard library when it provides a clear and maintainable solution.

External dependencies are acceptable when they materially improve correctness, interoperability, maintainability, or development effort.

Do not add frameworks merely to reproduce functionality already handled well by Go.

### Explicit Dependencies

Prefer explicit construction and dependency passing.

Avoid:

- service locators;
- hidden global dependencies;
- dependency injection frameworks;
- mutable package-level application state.

### Composition Root

Executable entry points under `cmd/` should primarily handle:

- configuration loading;
- dependency construction;
- process lifecycle;
- signal handling;
- application startup;
- graceful shutdown.

Business logic should not accumulate in `main.go`.

### Package Boundaries

Packages should represent meaningful responsibilities.

Do not create packages solely to mirror architectural diagrams.

Internal implementation packages should normally live under `internal/`.

Create a new package only when a real responsibility exists.

### Public API Stability

Treat externally consumed HTTP APIs and SDK contracts as product interfaces.

Do not leak:

- database schemas;
- Redis implementation details;
- LiveKit implementation details;
- internal identifiers unnecessarily;
- infrastructure-specific errors.

Prefer Convia-owned concepts at integration boundaries.

---

## Go Style

Follow standard idiomatic Go conventions.

Run:

```bash
go fmt ./...
```

before considering a change complete.

Also validate changes with:

```bash
go vet ./...
go test ./...
go build ./...
```

unless a task explicitly makes one of these commands inapplicable.

### Naming

Use concise, descriptive Go names.

Prefer:

```go
type Server struct {}
type Room struct {}
func NewServer(...) *Server
```

over verbose enterprise-style names.

Avoid names such as:

```go
ServerManagerService
RoomBusinessLogicHandler
AbstractCallProcessorFactory
```

unless the domain genuinely requires that distinction.

### Interfaces

Interfaces should normally be defined by the package that consumes the behavior.

Prefer small interfaces.

Do not create an interface solely because a concrete type exists.

### Errors

Errors are values.

Return errors explicitly and preserve useful context.

Prefer:

```go
return fmt.Errorf("load configuration: %w", err)
```

when wrapping an underlying error.

Do not:

- silently ignore errors;
- log an error and also return it without a clear reason;
- use `panic` for normal runtime failures.

`panic` is acceptable for genuinely unrecoverable programmer invariants, not ordinary application errors.

### Context

Use `context.Context` for request-scoped or operation-scoped cancellation, deadlines, and tracing.

Pass context explicitly.

Do not store request contexts permanently inside long-lived structs.

### Concurrency

Do not introduce goroutines merely because Go supports them.

Every goroutine must have:

- a clear owner;
- a clear lifetime;
- a cancellation or termination strategy where applicable.

Avoid goroutine leaks.

Prefer simple synchronous code until concurrency is necessary.

### Channels

Use channels for coordination when they naturally model the problem.

Do not use channels as a universal replacement for ordinary function calls, mutexes, or data structures.

---

## HTTP

The public API should follow stable HTTP semantics.

Use:

- appropriate HTTP methods;
- appropriate status codes;
- structured JSON responses;
- explicit content types;
- request validation;
- consistent error responses.

Do not expose raw internal error strings as public API contracts.

Handlers should remain thin.

Business behavior should eventually live outside transport-specific code.

---

## Configuration

Runtime configuration should come from explicit configuration sources such as environment variables.

Provide safe development defaults where appropriate.

Do not hard-code:

- production credentials;
- secrets;
- infrastructure addresses that vary by environment;
- API keys;
- database passwords.

Fail clearly when mandatory production configuration is absent.

---

## Security

Treat security as part of the architecture rather than a later feature.

Never commit:

- credentials;
- private keys;
- JWT signing secrets;
- database passwords;
- LiveKit API secrets;
- production tokens.

Authentication tokens should eventually be:

- scoped;
- short-lived where appropriate;
- validated server-side;
- issued according to least privilege.

External applications must eventually receive explicit permissions/scopes rather than implicit unrestricted access.

---

## Data

PostgreSQL is the planned source of truth for persistent application data.

Redis is intended for data whose characteristics justify it, such as:

- ephemeral state;
- distributed coordination;
- presence;
- short-lived cache;
- pub/sub or similar distributed communication.

Do not use Redis as the canonical source of truth for durable domain data.

Do not add either database until required by an implemented feature.

---

## Media

Do not implement raw media transport in the Go control-plane service.

The application backend should not proxy ordinary audio/video payloads through REST or WebSocket.

Media clients should eventually communicate with the media infrastructure using WebRTC.

Convia's backend is responsible for control, authorization, orchestration, and integration.

LiveKit should eventually be accessed through an internal adapter or gateway rather than throughout the domain codebase.

---

## Testing

Every meaningful behavior should be testable.

Prefer:

- unit tests for deterministic domain behavior;
- HTTP tests using `httptest`;
- integration tests for infrastructure boundaries where appropriate.

Tests must not depend on arbitrary sleep durations when synchronization can be made deterministic.

Avoid tests that require external network access unless they are explicitly integration tests.

Bug fixes should generally include a regression test when practical.

---

## Observability

The long-term observability direction is **OpenTelemetry**.

When observability is introduced, prefer structured telemetry suitable for production systems:

- structured logs;
- traces;
- metrics.

Do not scatter vendor-specific observability APIs throughout business logic.

Do not add a full observability stack before the service has behavior worth observing.

---

## Docker

Containerization should remain simple.

Production containers should:

- contain only what is required to run Convia;
- avoid development tooling;
- run as a non-root user where practical;
- use multi-stage builds where beneficial.

Do not introduce Kubernetes manifests until deployment requirements justify Kubernetes.

---

## Repository Evolution

The project is expected to grow incrementally.

Likely future areas include:

```text
applications
identity
auth
rooms
calls
participants
presence
permissions
tokens
webhooks
media
sdk
```

This list describes expected domain areas, not a required directory structure.

Do not create these packages until corresponding functionality is implemented.

---

## Initial Development Rule

During early development, optimize for:

1. correctness;
2. clear architecture;
3. testability;
4. maintainability;
5. observability;
6. performance.

Do not optimize prematurely.

However, avoid architectural choices that obviously prevent horizontal scaling or make distributed operation unnecessarily difficult later.

---

## Change Discipline

Before modifying code:

1. inspect the relevant existing implementation;
2. understand the current package responsibility;
3. preserve established conventions unless there is a concrete reason to change them.

After modifying code:

1. format changed Go code;
2. run relevant tests;
3. run static validation;
4. build the affected modules/packages;
5. report failures rather than hiding them.

For repository-wide Go changes, normally run:

```bash
go fmt ./...
go vet ./...
go test ./...
go build ./...
```

---

## Avoid

Unless explicitly required by the current task, do not introduce:

- premature microservices;
- Kubernetes;
- a dependency injection framework;
- a web framework;
- an ORM;
- event sourcing;
- CQRS;
- a message broker;
- GraphQL;
- custom WebRTC implementations;
- custom RTP implementations;
- custom SFU implementations;
- abstractions for unsupported hypothetical media providers;
- folders containing only placeholders.

The architecture should be capable of evolving toward a large distributed communication platform without pretending that the first version already is one.