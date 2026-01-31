#!/bin/bash
set -e

# Build script for crush-extended
# Called by goreleaser to build the extended Crush binary with plugins

GOOS=$1
GOARCH=$2
OUTPUT=$3

if [ -z "$GOOS" ] || [ -z "$GOARCH" ] || [ -z "$OUTPUT" ]; then
    echo "Usage: $0 <GOOS> <GOARCH> <output>"
    exit 1
fi

echo "Building crush-extended for ${GOOS}/${GOARCH}..."

# Build xcrush first
XCRUSH_TEMP=$(mktemp)
cd ../crush-plugin-poc
GOOS=$GOOS GOARCH=$GOARCH go build -o "$XCRUSH_TEMP" ./cmd/xcrush

# Use xcrush to build crush-extended
cd ../crush-modules
"$XCRUSH_TEMP" build \
    --crush ../crush-plugin-poc \
    --with ./otlp \
    --with ./agent-status \
    --with ./periodic-prompts \
    --output "$OUTPUT"

# Clean up
rm "$XCRUSH_TEMP"

echo "Built crush-extended: $OUTPUT"
