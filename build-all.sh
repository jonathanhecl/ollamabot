#!/usr/bin/env bash
# Compiles and packages ollamabot for all major operating systems and architectures.
# Usage:
#   ./build-all.sh
#   ./build-all.sh v1.0.0
set -euo pipefail

VERSION="${1:-}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIST="$ROOT/dist"

for cmd in go tar zip; do
    command -v "$cmd" >/dev/null 2>&1 || { echo "Error: $cmd is not installed or is not in PATH." >&2; exit 1; }
done

rm -rf "$DIST"
mkdir -p "$DIST"

LDFLAGS="-s -w -X 'main.buildTime=$(date '+%Y-%m-%d %H:%M:%S')'"
[ -n "$VERSION" ] && LDFLAGS="$LDFLAGS -X 'main.version=$VERSION'"

PACK_BASE="ollamabot"
[ -n "$VERSION" ] && PACK_BASE="$PACK_BASE-$VERSION"

# os arch format label
TARGETS="windows amd64 zip windows-amd64
windows arm64 zip windows-arm64
linux amd64 targz linux-amd64
linux arm64 targz linux-arm64
darwin amd64 targz darwin-amd64-intel
darwin arm64 targz darwin-arm64-applesilicon"

echo "Starting multiplatform build..."
cd "$ROOT"
while read -r os arch format label; do
    binary="ollamabot"
    [ "$os" = "windows" ] && binary="ollamabot.exe"
    tmp="$DIST/temp_${os}_${arch}"
    rm -rf "$tmp"
    mkdir -p "$tmp"

    echo "Building for $os ($arch)..."
    if ! CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags="$LDFLAGS" -o "$tmp/$binary" ./cmd/ollamabot; then
        echo "Error: build failed for $os/$arch" >&2
        rm -rf "$tmp"
        exit 1
    fi

    [ -f "$ROOT/.env.example" ] && cp "$ROOT/.env.example" "$tmp/"

    if [ "$format" = "zip" ]; then
        pack="$DIST/$PACK_BASE-$label.zip"
        (cd "$tmp" && zip -q -r "$pack" .)
    else
        pack="$DIST/$PACK_BASE-$label.tar.gz"
        (cd "$tmp" && COPYFILE_DISABLE=1 tar -czf "$pack" "$binary" .env.example)
    fi
    echo "Packaged $(basename "$pack") ($binary, .env.example)"
    rm -rf "$tmp"
done <<EOF
$TARGETS
EOF

echo ""
echo "Build and packaging completed successfully. Files in dist/:"
ls -l "$DIST"
