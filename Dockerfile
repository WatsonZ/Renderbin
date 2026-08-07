# syntax=docker/dockerfile:1

# Both build stages are pinned to $BUILDPLATFORM -- the builder's own
# architecture -- so a multi-arch build never runs them under QEMU. That works
# because the frontend output is architecture-independent (build it once) and
# the backend cross-compiles for free: CGO_ENABLED=0 plus the pure-Go SQLite
# driver mean targeting arm64 is just a different GOARCH. Only the small
# runtime stage below actually varies per platform.

FROM --platform=$BUILDPLATFORM node:22-alpine AS web-build
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS backend-build
ARG TARGETARCH
# Release builds pass the Git tag; a plain `docker build` leaves it as "dev".
ARG VERSION=dev
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=web-build /src/web/build ./internal/web/dist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/shawn-bluce/renderbin/backend/internal/buildinfo.Version=${VERSION}" \
      -o /out/server ./cmd/server

FROM alpine:3.22
# wget (busybox) is what HEALTHCHECK uses; ca-certificates and tzdata are for
# outbound TLS and local-time formatting.
RUN apk add --no-cache ca-certificates tzdata

# Run unprivileged. /data has to exist here, owned by app: Docker copies a
# named volume's initial ownership from the image directory it shadows, and
# only when it first creates the volume. Without this the volume lands
# root-owned and the server can't open its database.
RUN adduser -D -u 10001 app && mkdir -p /data && chown app:app /data
USER app

WORKDIR /app
COPY --from=backend-build /out/server ./server

ENV LISTEN_ADDR=:8080
ENV DB_PATH=/data/app.db
EXPOSE 8080

# Hardcodes the default port: override LISTEN_ADDR and the container reports
# unhealthy even though it works. restart: unless-stopped ignores health, so
# nothing restarts -- but adjust this if you change the port.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/health || exit 1

ENTRYPOINT ["./server"]
