# syntax=docker/dockerfile:1
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/extractcli ./cmd/extractcli

# Runtime: Poppler only (pdftotext reads digital PDFs, pdftoppm renders pages for the vision model).
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends poppler-utils ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --home /app app \
 && mkdir -p /data/storage /app && chown -R app /data /app
COPY --from=build /out/ /usr/local/bin/
USER app
WORKDIR /app
ENV STORAGE_DIR=/data/storage LISTEN_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["server"]
