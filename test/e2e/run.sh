#!/bin/bash
#
# E2E test runner for confab install command
# Runs test scenarios in Docker containers with isolated environments
#
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
IMAGE_NAME="confab-e2e-test"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

passed=0
failed=0
failed_tests=""

log_info() {
    echo -e "${YELLOW}==>${NC} $1"
}

log_pass() {
    echo -e "${GREEN}PASS${NC}: $1"
    ((passed++))
}

log_fail() {
    echo -e "${RED}FAIL${NC}: $1"
    ((failed++))
    failed_tests="$failed_tests\n  - $1"
}

# Build the confab binary for Linux
build_binary() {
    log_info "Building confab binary for Linux..."
    cd "$PROJECT_ROOT"
    GOOS=linux GOARCH=amd64 go build -o test/e2e/confab-linux
    log_info "Binary built: test/e2e/confab-linux"
}

# Build the Docker image
build_image() {
    log_info "Building Docker image..."
    docker build -t "$IMAGE_NAME" "$SCRIPT_DIR"
}

# Run a single test scenario
# Args: $1 = scenario name, $2 = shell, $3 = env vars (optional)
run_scenario() {
    local scenario="$1"
    local shell="$2"
    local env_args="$3"
    local test_name="${scenario} (${shell})"

    log_info "Running: $test_name"

    # Run the scenario in Docker
    if docker run --rm \
        -v "$SCRIPT_DIR/confab-linux:/home/testuser/confab:ro" \
        -v "$SCRIPT_DIR/scenarios:/home/testuser/scenarios:ro" \
        $env_args \
        "$IMAGE_NAME" \
        "$shell" "/home/testuser/scenarios/${scenario}.sh" 2>&1; then
        log_pass "$test_name"
        return 0
    else
        log_fail "$test_name"
        return 1
    fi
}

# Main
main() {
    echo "========================================"
    echo "  Confab Install Command E2E Tests"
    echo "========================================"
    echo

    build_binary
    build_image

    echo
    log_info "Running test scenarios..."
    echo

    # Test 1: Fresh install (no ~/.local/bin, not on PATH)
    run_scenario "fresh-install" "/bin/bash" || true

    # Test 2: Directory exists but not on PATH
    run_scenario "dir-exists-not-in-path" "/bin/bash" || true

    # Test 3: Already on PATH
    run_scenario "already-on-path" "/bin/bash" "-e PATH=/home/testuser/.local/bin:/usr/bin:/bin" || true

    # Test 4: Custom --dest flag
    run_scenario "custom-dest" "/bin/bash" || true

    # Test 5: Re-install (binary already exists)
    run_scenario "reinstall" "/bin/bash" || true

    # Test 6: Different shells - zsh
    run_scenario "shell-detection" "/bin/zsh" "-e SHELL=/bin/zsh" || true

    # Test 7: Different shells - fish
    run_scenario "shell-detection-fish" "/usr/bin/fish" "-e SHELL=/usr/bin/fish" || true

    # Test 8: Different shells - bash
    run_scenario "shell-detection" "/bin/bash" "-e SHELL=/bin/bash" || true

    # Summary
    echo
    echo "========================================"
    echo "  Test Summary"
    echo "========================================"
    echo -e "  ${GREEN}Passed${NC}: $passed"
    echo -e "  ${RED}Failed${NC}: $failed"

    if [ $failed -gt 0 ]; then
        echo -e "\nFailed tests:$failed_tests"
        exit 1
    fi

    echo
    echo "All tests passed!"
}

main "$@"
