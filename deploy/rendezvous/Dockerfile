FROM golang:1.26.9-alpine@sha256:3082400e369fa24d5fc60bca20edab3f6d604e0c5a690ec66b295eff4dd87ade AS build
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
