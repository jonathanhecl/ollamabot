#!/bin/bash

set -e

APP_NAME="ollamabot"
OUTPUT_DIR="dist"

echo "Building $APP_NAME for macOS..."

mkdir -p "$OUTPUT_DIR"

# macOS Intel (amd64)
echo "  -> macOS amd64 (Intel)..."
GOOS=darwin GOARCH=amd64 go build -o "$OUTPUT_DIR/${APP_NAME}-darwin-amd64" ./cmd/ollamabot

# macOS Apple Silicon (arm64)
echo "  -> macOS arm64 (Apple Silicon)..."
GOOS=darwin GOARCH=arm64 go build -o "$OUTPUT_DIR/${APP_NAME}-darwin-arm64" ./cmd/ollamabot

echo ""
echo "Build completed successfully:"
ls -lh "$OUTPUT_DIR"/"$APP_NAME"-darwin-*
