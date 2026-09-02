# syntax=docker/dockerfile:1.7

FROM golang:1.24.13-alpine3.23 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go test ./...

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/gedis ./cmd/gedis && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/gedis-healthcheck ./cmd/gedis-healthcheck && \
    mkdir -p /out/data

FROM scratch

LABEL org.opencontainers.image.source="https://github.com/mrktsm/gedis" \
      org.opencontainers.image.description="A Redis-compatible in-memory database implemented in Go" \
      org.opencontainers.image.licenses="Apache-2.0"

COPY --from=build /out/gedis /usr/local/bin/gedis
COPY --from=build /out/gedis-healthcheck /usr/local/bin/gedis-healthcheck
COPY --from=build --chown=65532:65532 /out/data /data

USER 65532:65532
VOLUME ["/data"]
EXPOSE 6379
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=5s --timeout=2s --start-period=2s --retries=5 \
    CMD ["/usr/local/bin/gedis-healthcheck", "-addr", "127.0.0.1:6379", "-timeout", "1s"]

ENTRYPOINT ["/usr/local/bin/gedis"]
CMD ["-addr", "0.0.0.0:6379"]
