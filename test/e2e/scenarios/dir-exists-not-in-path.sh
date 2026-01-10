#!/bin/bash
# Test: Directory exists but not on PATH
set -e

echo "=== Test: Directory Exists, Not in PATH ==="

# Create the directory but ensure it's not in PATH
mkdir -p ~/.local/bin
export PATH="/usr/bin:/bin"

# Verify directory exists
if [ ! -d ~/.local/bin ]; then
    echo "FAIL: ~/.local/bin should exist"
    exit 1
fi

# Run install
OUTPUT=$(~/confab install 2>&1)
echo "$OUTPUT"

# Verify binary was installed
if [ ! -f ~/.local/bin/confab ]; then
    echo "FAIL: Binary not installed to ~/.local/bin/confab"
    exit 1
fi

# Verify output mentions PATH not being set
if ! echo "$OUTPUT" | grep -q "not in your PATH"; then
    echo "FAIL: Should warn that directory is not in PATH"
    exit 1
fi

echo "=== PASSED ==="
