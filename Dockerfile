# syntax=docker/dockerfile:1

FROM golang:1.25.5-alpine AS builder

ARG VERSION=0.2.0

WORKDIR /src

# Download dependencies first so they are cached independently of source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/loki-mcp-server ./cmd/server

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S mcp \
    && adduser -S -G mcp -H -s /sbin/nologin mcp

WORKDIR /app

COPY --from=builder /out/loki-mcp-server /usr/local/bin/loki-mcp-server

USER mcp

# The image is built for HTTP deployment and serves the Streamable HTTP
# endpoint on /mcp. Set MCP_TRANSPORT=stdio to use it as a stdio server.
ENV MCP_TRANSPORT=http
ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["loki-mcp-server"]
