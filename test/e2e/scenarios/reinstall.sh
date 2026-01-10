#!/bin/bash
# Test: Re-install when binary already exists
set -e

echo "=== Test: Re-install ==="

# First install
mkdir -p ~/.local/bin
~/confab install > /dev/null 2>&1

# Verify first install worked
if [ ! -f ~/.local/bin/confab ]; then
    echo "FAIL: First install failed"
    exit 1
fi

# Get original file info
ORIG_SIZE=$(stat -c %s ~/.local/bin/confab 2>/dev/null || stat -f %z ~/.local/bin/confab)

# Run install again
OUTPUT=$(~/confab install 2>&1)
echo "$OUTPUT"

# Verify binary still exists
if [ ! -f ~/.local/bin/confab ]; then
    echo "FAIL: Binary should still exist after reinstall"
    exit 1
fi

# Verify binary still works
if ! ~/.local/bin/confab --help > /dev/null 2>&1; then
    echo "FAIL: Reinstalled binary does not work"
    exit 1
fi

# Verify output indicates success
if ! echo "$OUTPUT" | grep -q "installed"; then
    echo "FAIL: Should indicate installation success"
    exit 1
fi

echo "=== PASSED ==="
