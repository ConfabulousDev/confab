#!/bin/bash
# Test: Custom --dest flag
set -e

echo "=== Test: Custom Destination ==="

CUSTOM_DIR="$HOME/my-custom-bin"

# Ensure clean state
rm -rf "$CUSTOM_DIR"

# Run install with custom destination
OUTPUT=$(~/confab install --dest "$CUSTOM_DIR" 2>&1)
echo "$OUTPUT"

# Verify binary was installed to custom location
if [ ! -f "$CUSTOM_DIR/confab" ]; then
    echo "FAIL: Binary not installed to $CUSTOM_DIR/confab"
    exit 1
fi

# Verify binary is executable
if [ ! -x "$CUSTOM_DIR/confab" ]; then
    echo "FAIL: Binary is not executable"
    exit 1
fi

# Verify binary works
if ! "$CUSTOM_DIR/confab" --help > /dev/null 2>&1; then
    echo "FAIL: Installed binary does not work"
    exit 1
fi

# Verify default location was NOT used
if [ -f ~/.local/bin/confab ]; then
    echo "FAIL: Binary should not be in default location"
    exit 1
fi

# Verify output mentions the custom directory
if ! echo "$OUTPUT" | grep -q "$CUSTOM_DIR"; then
    echo "FAIL: Output should reference custom directory"
    exit 1
fi

echo "=== PASSED ==="
