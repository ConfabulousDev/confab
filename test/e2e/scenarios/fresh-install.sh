#!/bin/bash
# Test: Fresh install - no ~/.local/bin directory, not on PATH
set -e

echo "=== Test: Fresh Install ==="

# Ensure clean state
rm -rf ~/.local/bin

# Verify ~/.local/bin does not exist
if [ -d ~/.local/bin ]; then
    echo "FAIL: ~/.local/bin should not exist"
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

# Verify binary is executable
if [ ! -x ~/.local/bin/confab ]; then
    echo "FAIL: Binary is not executable"
    exit 1
fi

# Verify binary works
if ! ~/.local/bin/confab --help > /dev/null 2>&1; then
    echo "FAIL: Installed binary does not work"
    exit 1
fi

# Verify output mentions PATH not being set (since we're in a fresh container)
if ! echo "$OUTPUT" | grep -q "not in your PATH"; then
    echo "FAIL: Should warn that directory is not in PATH"
    exit 1
fi

# Verify one-liner instruction is shown
if ! echo "$OUTPUT" | grep -q "echo.*>>"; then
    echo "FAIL: Should show one-liner command for adding to PATH"
    exit 1
fi

echo "=== PASSED ==="
