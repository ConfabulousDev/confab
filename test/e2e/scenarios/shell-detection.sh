#!/bin/bash
# Test: Shell detection for PATH instructions (bash/zsh)
set -e

echo "=== Test: Shell Detection ==="

SHELL_NAME=$(basename "$SHELL")
echo "Testing with SHELL=$SHELL (name: $SHELL_NAME)"

# Ensure clean state and PATH doesn't include ~/.local/bin
rm -rf ~/.local/bin
export PATH="/usr/bin:/bin"

# Run install
OUTPUT=$(~/confab install 2>&1)
echo "$OUTPUT"

# Verify shell-specific config file is mentioned in one-liner
case "$SHELL_NAME" in
    bash)
        if ! echo "$OUTPUT" | grep -q ">> ~/\.bashrc"; then
            echo "FAIL: Should mention .bashrc for bash shell"
            exit 1
        fi
        ;;
    zsh)
        if ! echo "$OUTPUT" | grep -q ">> ~/\.zshrc"; then
            echo "FAIL: Should mention .zshrc for zsh shell"
            exit 1
        fi
        ;;
    *)
        echo "Unknown shell: $SHELL_NAME, checking for generic output"
        if ! echo "$OUTPUT" | grep -q "export PATH"; then
            echo "FAIL: Should show export PATH instruction"
            exit 1
        fi
        ;;
esac

# Verify it's a one-liner with && source to reload shell config
if ! echo "$OUTPUT" | grep -q "&& source"; then
    echo "FAIL: Should include && source to reload shell config"
    exit 1
fi

echo "=== PASSED ==="
