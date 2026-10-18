# syntax=docker/dockerfile:1

# 1. Build the React frontend
FROM node:20-alpine AS web
WORKDIR /web
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# 2. Build the Go server (static binary, no CGO)
FROM golang:1.24-alpine AS api
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server .

# 3. Small runtime image that runs as a non-root user
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=api /out/server /app/server
COPY --from=web /web/dist /app/public
ENV PORT=8080 STATIC_DIR=/app/public
EXPOSE 8080
USER nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/app/server", "healthcheck"]
ENTRYPOINT ["/app/server"]
