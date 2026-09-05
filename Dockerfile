FROM golang:1.26.6-alpine AS build

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/convia ./cmd/convia

FROM scratch

COPY --from=build /out/convia /convia

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/convia"]

