# Convia's interface is compiled into the binary, so the image builds it first.
#
# The frontend stage runs before the Go stage and writes into the directory the
# Go package embeds. Both stages start from their dependency manifests alone, so
# a change to source code does not invalidate the cached dependency install.
FROM node:24.11-alpine AS interface

WORKDIR /src

# The workspace is installed whole, because `npm ci` resolves all of it or none.
#
# One lockfile lives at the root and each member's package.json has to be
# present for it to match, so all three manifests are copied before any source.
# Only the interface's source follows: the SDK is a sibling the interface does
# not import yet, and copying it would make a change to it rebuild this.
COPY package.json package-lock.json ./
COPY web/package.json ./web/package.json
COPY sdk/package.json ./sdk/package.json
RUN npm ci

COPY web ./web
RUN npm run build --workspace convia-web

FROM golang:1.26.6-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

# The bundle, from the stage above. Without this the binary still builds and
# still serves the API; it answers 503 with a page saying the interface was not
# built into it.
COPY --from=interface /src/internal/web/assets/dist ./internal/web/assets/dist

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/convia ./cmd/convia

FROM scratch

COPY --from=build /out/convia /convia

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/convia"]
