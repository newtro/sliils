# syntax=docker/dockerfile:1.7

# --- build stage ---
FROM golang:1.24-alpine AS build

WORKDIR /src

# Cache modules first
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/sliils-app ./cmd/sliils-app

# --- runtime stage ---
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S sliils && adduser -S -G sliils sliils

COPY --from=build /out/sliils-app /usr/local/bin/sliils-app

USER sliils
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q --spider http://localhost:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/sliils-app"]
CMD ["--mode=all"]
