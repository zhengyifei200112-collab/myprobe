# syntax=docker/dockerfile:1
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/myprobe-server ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/myprobe-agent ./cmd/agent

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S myprobe && adduser -S -G myprobe -h /var/lib/myprobe myprobe && \
    mkdir -p /var/lib/myprobe && chown myprobe:myprobe /var/lib/myprobe
COPY --from=build /out/myprobe-server /usr/local/bin/myprobe-server
COPY --from=build /out/myprobe-agent /usr/local/bin/myprobe-agent
USER myprobe
WORKDIR /var/lib/myprobe
VOLUME ["/var/lib/myprobe"]
EXPOSE 25775
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:25775/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/myprobe-server"]
