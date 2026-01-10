#!/usr/bin/fish
# Test: Shell detection for fish shell
# Fish has different syntax for PATH manipulation

echo "=== Test: Shell Detection (fish) ==="

echo "Testing with SHELL=$SHELL"

# Ensure clean state
rm -rf ~/.local/bin

# Set PATH without ~/.local/bin
set -gx PATH /usr/bin /bin

# Run install
set OUTPUT (~/confab install 2>&1)
echo $OUTPUT

# Verify fish-specific config file is mentioned in one-liner
if not string match -q "*>> ~/.config/fish/config.fish*" "$OUTPUT"
    echo "FAIL: Should mention config.fish for fish shell"
    exit 1
end

# Verify fish uses && source to reload config
if not string match -q "*&& source ~/.config/fish/config.fish*" "$OUTPUT"
    echo "FAIL: Should show && source to reload fish config"
    exit 1
end

# Verify binary was installed
if not test -f ~/.local/bin/confab
    echo "FAIL: Binary not installed"
    exit 1
end

echo "=== PASSED ==="
