# syntax=docker/dockerfile:1
#
# Budgie BBS single-host image for Fly.io: builds the SPA and the static Go
# binary, then ships both. The SQLite DB, JWT secret and SSH host key live on a
# mounted volume at /data (see fly.toml). Build context is the repo root.

# --- web build ---
FROM node:20-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# --- go build (pure Go: modernc SQLite, no CGO) ---
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/budgied ./cmd/budgied

# --- runtime ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl && adduser -D -u 10001 budgie
COPY --from=build /out/budgied /usr/local/bin/budgied
COPY --from=web   /web/dist     /web/dist
RUN install -d -o budgie -g budgie /data
USER budgie
# 8081 HTTP (Fly edge terminates TLS), 2222 SSH TUI.
EXPOSE 8081 2222
# JWT secret comes from the BUDGIE_JWT_SECRET Fly secret. DB/secret/hostkey on
# the /data volume so they persist across deploys.
ENTRYPOINT ["budgied", "-storage", "sqlite", \
  "-db", "/data/budgie.db", \
  "-hostkey", "/data/budgie_host_key", \
  "-http", ":8081", "-ssh", ":2222", \
  "-web", "/web/dist"]
