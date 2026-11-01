FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
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
