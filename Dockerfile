# syntax=docker/dockerfile:1

# --- build stage ---
# budgied is pure Go (modernc.org/sqlite + lib/pq), so CGO is not required.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/budgied ./cmd/budgied

# --- runtime stage ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl && adduser -D -u 10001 budgie
COPY --from=build /out/budgied /usr/local/bin/budgied
USER budgie
WORKDIR /home/budgie
# 8080 HTTP/WS/SSE + ops endpoints, 2222 SSH TUI, 1190 NNTP (optional).
EXPOSE 8080 2222 1190
ENTRYPOINT ["budgied"]
