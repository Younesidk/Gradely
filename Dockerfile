# 1. Build the Svelte app. Vite writes into ../backend/internal/web/dist, which the Go binary embeds.
FROM node:24-alpine AS web
WORKDIR /web
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# 2. Build one static Go binary (pure-Go SQLite driver, no CGO).
FROM golang:1.27-alpine AS build
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=web /backend/internal/web/dist ./internal/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gradly .

# 3. Tiny runtime image. The entrypoint fixes ownership of the data volume, then drops root.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata su-exec \
    && adduser -D -u 10001 gradly \
    && mkdir /data && chown gradly:gradly /data
COPY --from=build /out/gradly /usr/local/bin/gradly
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

ENV GRADLY_ADDR=:8080 \
    GRADLY_DATA_DIR=/data \
    GRADLY_SECURE_COOKIES=true
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD wget -q -O /dev/null http://127.0.0.1:8080/ || exit 1
ENTRYPOINT ["docker-entrypoint.sh"]
