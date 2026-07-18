FROM oven/bun:1.3.12 AS web-build

WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web ./
RUN bun run build

FROM golang:1.26.5-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN rm -rf internal/webui/assets
COPY --from=web-build /src/web/dist ./internal/webui/assets
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/windforce-core ./cmd/windforce-core

FROM python:3.14.6-slim-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

RUN useradd --system --uid 10001 --create-home windforce \
    && mkdir -p /data/store /data/cache \
    && chown -R windforce:windforce /data

COPY --from=build /out/windforce-core /usr/local/bin/windforce-core
COPY --from=web-build /usr/local/bin/bun /usr/local/bin/bun

USER windforce
WORKDIR /data
EXPOSE 8080

ENTRYPOINT ["windforce-core"]
CMD ["api", "--addr", ":8080", "--store", "/data/store", "--catalog", "/data/catalog.json", "--git-sources", "/data/git-sources.json"]
