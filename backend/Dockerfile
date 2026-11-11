# syntax=docker/dockerfile:1
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/extractcli ./cmd/extractcli

# PaddlePaddle is pinned to 3.2.2: 3.3.x crashes on CPU with oneDNN enabled (PIR attribute bug), and
# without oneDNN inference is ~6x slower.
# Runtime: Poppler (pdftotext/pdftoppm), PaddleOCR (primary OCR) and Tesseract (fallback).
FROM python:3.11-slim-bookworm
RUN apt-get update \
 && apt-get install -y --no-install-recommends poppler-utils tesseract-ocr tesseract-ocr-eng \
      libgl1 libglib2.0-0 libgomp1 ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/*
ARG PADDLEPADDLE_VERSION=3.2.2
ARG PADDLEOCR_VERSION=3.7.0
RUN pip install --no-cache-dir "paddlepaddle==${PADDLEPADDLE_VERSION}" "paddleocr==${PADDLEOCR_VERSION}"

# Bake the PP-OCRv5 models into the image so the worker never downloads at runtime.
ENV PADDLE_PDX_CACHE_HOME=/opt/paddlex PADDLE_PDX_DISABLE_MODEL_SOURCE_CHECK=True
ARG PADDLE_OCR_MODEL=mobile
RUN python -c "from paddleocr import PaddleOCR; PaddleOCR(use_doc_orientation_classify=False, use_doc_unwarping=False, use_textline_orientation=False, text_detection_model_name='PP-OCRv5_${PADDLE_OCR_MODEL}_det', text_recognition_model_name='PP-OCRv5_${PADDLE_OCR_MODEL}_rec')" \
 && chmod -R a+rX /opt/paddlex

RUN useradd --system --uid 10001 --home /app app \
 && mkdir -p /data/storage /app/ocr && chown -R app /data /app
COPY --from=build /out/ /usr/local/bin/
COPY ocr/paddle_ocr_server.py /app/ocr/paddle_ocr_server.py
USER app
WORKDIR /app
ENV STORAGE_DIR=/data/storage LISTEN_ADDR=:8080 \
    OCR_ENGINE=paddle PADDLE_PYTHON=python PADDLE_OCR_SCRIPT=/app/ocr/paddle_ocr_server.py PADDLE_OCR_MODEL=${PADDLE_OCR_MODEL} \
    OMP_NUM_THREADS=4
EXPOSE 8080
ENTRYPOINT ["server"]
CMD ["api"]
