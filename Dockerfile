FROM oven/bun:1.3.12 AS web-build

WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web ./
RUN bun run build:next

FROM golang:1.23-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN rm -rf internal/webui/assets
COPY --from=web-build /src/web/out ./internal/webui/assets
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/windforce-lite ./cmd/windforce-lite

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git python3 \
    && rm -rf /var/lib/apt/lists/*

RUN useradd --system --uid 10001 --create-home windforce \
    && mkdir -p /data/store /data/cache \
    && chown -R windforce:windforce /data

COPY --from=build /out/windforce-lite /usr/local/bin/windforce-lite

USER windforce
WORKDIR /data
EXPOSE 8080

ENTRYPOINT ["windforce-lite"]
CMD ["api", "--addr", ":8080", "--store", "/data/store", "--catalog", "/data/catalog.json", "--git-sources", "/data/git-sources.json"]
