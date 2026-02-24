# syntax=docker/dockerfile:1.7

FROM golang:1.24-bookworm AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/tyr-blog-img ./cmd/server

FROM debian:bookworm-slim AS runtime

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates webp \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /out/tyr-blog-img /app/tyr-blog-img

ENV LISTEN_ADDR=:8080
EXPOSE 8080

CMD ["/app/tyr-blog-img"]
