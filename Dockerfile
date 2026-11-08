FROM node:24-bookworm-slim AS frontend-build
WORKDIR /build/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/index.html frontend/tsconfig.json frontend/vite.config.ts ./
COPY frontend/src ./src
RUN npm run typecheck && npm run build

FROM golang:1.26-bookworm AS backend-build
WORKDIR /build/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/cmd ./cmd
COPY backend/internal ./internal
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/watrik ./cmd/watrik

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 watrik \
    && useradd --uid 10001 --gid 10001 --no-create-home --home-dir /app --shell /usr/sbin/nologin watrik \
    && install -d -m 0700 -o 10001 -g 10001 /app/data
WORKDIR /app
COPY --from=backend-build /out/watrik /app/watrik
COPY --from=frontend-build /build/frontend/dist /app/dist
ENV WATRIK_ADDR=:8080 \
    WATRIK_DATABASE_PATH=/app/data/watrik.db \
    WATRIK_STATIC_DIR=/app/dist
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/watrik"]
