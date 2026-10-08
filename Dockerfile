# ── build ────────────────────────────────────────────────────
FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO disabled: pure-Go SQLite driver → fully static binary, no libc.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/modeltrack ./cmd/modeltrack

# ── runtime ──────────────────────────────────────────────────
FROM alpine:3.20

RUN adduser -D -u 1000 modeltrack && mkdir -p /data && chown -R modeltrack /data
COPY --from=build /out/modeltrack /usr/local/bin/modeltrack

USER modeltrack
ENV DB_PATH=/data/modeltrack.db
EXPOSE 8000

ENTRYPOINT ["/usr/local/bin/modeltrack"]
