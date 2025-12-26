# Copyright (c) 2025 Berik Ashimov

# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS builder
WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download || true

COPY . .
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/subnetio ./cmd/subnetio

FROM alpine:3.20
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /out/subnetio /app/subnetio

VOLUME ["/data"]
ENV DB_PATH=/data/subnetio.sqlite
ENV LISTEN_ADDR=0.0.0.0:8080

EXPOSE 8080
ENTRYPOINT ["/app/subnetio"]
