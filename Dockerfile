# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS builder

WORKDIR /src
COPY go.mod ./
COPY . .

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/lfh .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata \
    && mkdir -p /data

WORKDIR /app
COPY --from=builder /out/lfh /usr/local/bin/lfh

EXPOSE 8080/tcp 53319/udp
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/info >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/lfh"]
CMD ["-data", "/data"]
