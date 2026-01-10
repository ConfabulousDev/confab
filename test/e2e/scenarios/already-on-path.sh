#!/bin/bash
# Test: ~/.local/bin already on PATH
set -e

echo "=== Test: Already on PATH ==="

# Create the directory and ensure it's in PATH
mkdir -p ~/.local/bin
# PATH is set via docker run -e flag

# Verify PATH includes ~/.local/bin
if [[ ":$PATH:" != *":$HOME/.local/bin:"* ]]; then
    echo "FAIL: PATH should include ~/.local/bin (PATH=$PATH)"
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

# Verify output says PATH is already set
if ! echo "$OUTPUT" | grep -q "already in your PATH"; then
    echo "FAIL: Should confirm that directory is already in PATH"
    exit 1
fi

# Verify it does NOT show add instructions
if echo "$OUTPUT" | grep -q "echo.*>>"; then
    echo "FAIL: Should not show instructions when already on PATH"
    exit 1
fi

echo "=== PASSED ==="
