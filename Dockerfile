FROM alpine:3.21

WORKDIR /app

# Docker buildx 会在构建时自动填充这些变量
ARG TARGETOS
ARG TARGETARCH

RUN apk add --no-cache tzdata

COPY komari-${TARGETOS}-${TARGETARCH} /app/komari

RUN chmod +x /app/komari

ENV GIN_MODE=release
ENV KOMARI_DB_TYPE=sqlite
ENV KOMARI_DB_FILE=/app/data/komari.db
ENV KOMARI_DB_HOST=localhost
ENV KOMARI_DB_PORT=
ENV KOMARI_DB_USER=
ENV KOMARI_DB_PASS=
ENV KOMARI_DB_NAME=komari
ENV KOMARI_LISTEN=0.0.0.0:25774

EXPOSE 25774

CMD ["/app/komari", "server"]
