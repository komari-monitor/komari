FROM alpine:3.21

WORKDIR /app

# Docker buildx 会在构建时自动填充这些变量
ARG TARGETOS
ARG TARGETARCH

RUN apk add --no-cache ca-certificates curl tzdata

RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) cloudflared_arch="amd64" ;; \
      386) cloudflared_arch="386" ;; \
      arm64) cloudflared_arch="arm64" ;; \
      arm) cloudflared_arch="arm" ;; \
      *) echo "Unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${cloudflared_arch}" -o /usr/local/bin/cloudflared; \
    chmod +x /usr/local/bin/cloudflared

COPY komari-${TARGETOS}-${TARGETARCH} /app/komari

RUN chmod +x /app/komari

ENV GIN_MODE=release
ENV KOMARI_DB_TYPE=sqlite
ENV KOMARI_DB_FILE=/app/data/komari.db
ENV KOMARI_DB_DSN=
ENV KOMARI_LISTEN=0.0.0.0:25774

EXPOSE 25774

CMD ["/app/komari", "server"]
