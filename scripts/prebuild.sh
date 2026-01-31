#!/bin/bash
set -e

# Pre-build script for goreleaser
# Builds crush-extended binaries for all target platforms before goreleaser runs

echo "Building xcrush..."
cd crush-plugin-poc
go build -o ../dist/xcrush ./cmd/xcrush

echo "Building crush-extended for all platforms..."
cd ..

# Linux AMD64
echo "  - linux/amd64..."
./dist/xcrush build \
    --crush ./crush-plugin-poc \
    --with ./otlp \
    --with ./agent-status \
    --with ./periodic-prompts \
    --output ./dist/crush-extended_linux_amd64

# Linux ARM64
echo "  - linux/arm64..."
GOARCH=arm64 ./dist/xcrush build \
    --crush ./crush-plugin-poc \
    --with ./otlp \
    --with ./agent-status \
    --with ./periodic-prompts \
    --output ./dist/crush-extended_linux_arm64

# Darwin AMD64
echo "  - darwin/amd64..."
GOOS=darwin ./dist/xcrush build \
    --crush ./crush-plugin-poc \
    --with ./otlp \
    --with ./agent-status \
    --with ./periodic-prompts \
    --output ./dist/crush-extended_darwin_amd64

# Darwin ARM64
echo "  - darwin/arm64..."
GOOS=darwin GOARCH=arm64 ./dist/xcrush build \
    --crush ./crush-plugin-poc \
    --with ./otlp \
    --with ./agent-status \
    --with ./periodic-prompts \
    --output ./dist/crush-extended_darwin_arm64

echo "Pre-build complete!"
