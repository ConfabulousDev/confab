# Base image for e2e install command tests
# Provides bash, zsh, and fish shells for testing PATH detection

FROM ubuntu:22.04

# Install shells and basic utilities
RUN apt-get update && apt-get install -y \
    bash \
    zsh \
    fish \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user for testing
RUN useradd -m -s /bin/bash testuser

# Switch to non-root user
USER testuser
WORKDIR /home/testuser

# Binary will be mounted at /home/testuser/confab
# Scenarios will be mounted at /home/testuser/scenarios
