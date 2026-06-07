#!/bin/bash

set -e

APP_NAME="ollamabot"
OUTPUT_DIR="dist"

echo "Building $APP_NAME for macOS..."

mkdir -p "$OUTPUT_DIR"


echo "  -> macOS arm64 (Apple Silicon)..."
GOOS=darwin GOARCH=arm64 go build -o "$OUTPUT_DIR/${APP_NAME}" ./cmd/ollamabot

echo ""
echo "Build completed successfully:"
ls -lh "$OUTPUT_DIR"/"$APP_NAME"
