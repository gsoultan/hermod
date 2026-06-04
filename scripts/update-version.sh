#!/bin/bash
# scripts/update-version.sh

set -e

# Get latest tag from git
TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "dev")

if [ "$TAG" = "dev" ]; then
    echo "No tags found, using 'dev'"
fi

FILE="internal/version/version.go"

# Update Version variable in Go file
# We use a temp file for compatibility across different sed versions (macOS/Linux)
sed "s/var Version = \".*\"/var Version = \"$TAG (Enterprise Edition)\"/" "$FILE" > "${FILE}.tmp" && mv "${FILE}.tmp" "$FILE"

echo "Updated $FILE to $TAG"
