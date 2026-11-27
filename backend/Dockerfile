FROM golang:1.26.0 AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /invoice-backend ./cmd/server

FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends poppler-utils ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/*

RUN useradd --system --uid 10001 app \
 && mkdir -p /data/storage \
 && chown app /data/storage

COPY --from=build /invoice-backend /usr/local/bin/invoice-backend

USER app
ENV STORAGE_DIR=/data/storage LISTEN_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["invoice-backend"]