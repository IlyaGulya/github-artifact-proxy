FROM golang:1.23-alpine AS builder

WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 go build -o /github-artifact-proxy ./cmd/github-artifact-proxy

FROM alpine:latest

RUN adduser -D -H -h /app appuser && \
    mkdir -p /app && \
    chown -R appuser:appuser /app

COPY --from=builder /github-artifact-proxy /app/

USER appuser
WORKDIR /app
EXPOSE 8080

ENTRYPOINT ["/app/github-artifact-proxy"]
CMD ["--http-addr", ":8080", "--config", "/app/config.yml"]
