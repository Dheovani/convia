# Convia's interface is compiled into the binary, so the image builds it first.
#
# The frontend stage runs before the Go stage and writes into the directory the
# Go package embeds. Both stages start from their dependency manifests alone, so
# a change to source code does not invalidate the cached dependency install.
FROM node:24.11-alpine AS interface

WORKDIR /src/web

COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web ./
RUN npm run build

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
