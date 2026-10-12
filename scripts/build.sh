#!/bin/sh
# Builds the screen, embeds it and writes the Windows and local programs to dist/.
set -eu
cd "$(dirname "$0")/.."
version="${VERSION:-$(date +%Y%m%d)}"

(cd web && npm ci --no-audit --no-fund >/dev/null && npm run build >/dev/null)
find internal/webui/dist -mindepth 1 ! -name .keep -exec rm -rf {} +
cp -R web/dist/. internal/webui/dist/

mkdir -p dist
ldflags="-s -w -X main.version=$version"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$ldflags" -o dist/listing-tracker.exe ./cmd/listing-tracker
CGO_ENABLED=0 go build -trimpath -ldflags "$ldflags" -o dist/listing-tracker ./cmd/listing-tracker
ls -l dist
