FROM golang:1.26.10-alpine@sha256:9ce231b28998b32e116927c61aa5c406183061d4f3f2d61b292c88e7f6bdf7ca AS build
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
