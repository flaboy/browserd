# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -o /out/browserd ./cmd/browserd

FROM alpine:3.20
RUN apk add --no-cache \
    chromium \
    nss \
    freetype \
    harfbuzz \
    ca-certificates \
    ttf-freefont
RUN addgroup -S app && adduser -S app -G app
USER app
WORKDIR /app
COPY --from=builder /out/browserd /app/browserd
ENV CHROME_BIN=/usr/bin/chromium-browser
EXPOSE 7011
ENTRYPOINT ["/app/browserd"]
