FROM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /rendezvous ./cmd/rendezvous

FROM scratch
COPY --from=build /rendezvous /rendezvous
USER 65534:65534
ENTRYPOINT ["/rendezvous"]
