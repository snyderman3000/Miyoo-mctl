#!/bin/sh
# Builds the OnionOS package: dist/Miyoo-mctl-v<version>-OnionOS.zip
set -e
cd "$(dirname "$0")"
VERSION=$(grep -o 'const version = "[^"]*"' main.go | cut -d'"' -f2)
[ -f go.work ] || go mod tidy
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags="-s -w" -o package/Miyoo-mctl/mctl .
go run . -icon package/Miyoo-mctl/icon.png
rm -rf dist && mkdir -p dist/stage/App
cp -r package/Miyoo-mctl dist/stage/App/
rm -rf dist/stage/App/Miyoo-mctl/data
(cd dist/stage && zip -qr "../Miyoo-mctl-v$VERSION-OnionOS.zip" App)
rm -rf dist/stage
echo "Built dist/Miyoo-mctl-v$VERSION-OnionOS.zip"
