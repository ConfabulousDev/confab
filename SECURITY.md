# Security Policy

## Supported Versions

Only the latest release is supported with security updates.

| Version | Supported          |
| ------- | ------------------ |
| Latest  | :white_check_mark: |
| < Latest | :x:               |

## Reporting a Vulnerability

We take security vulnerabilities seriously. Please report them through GitHub Security Advisories:

1. Go to the [Security tab](../../security/advisories) of this repository
2. Click "Report a vulnerability"
3. Provide details including:
   - Description of the vulnerability
   - Steps to reproduce
   - Potential impact
   - Your suggested fix (if any)

### What to Expect

- **Acknowledgment**: Within 72 hours
- **Updates**: We'll keep you informed of our progress
- **Resolution**: We aim to fix critical issues within 30 days, others within 90 days
- **Disclosure**: Coordinated disclosure after fix is released

### Scope

**In scope:**
- The `confab` CLI binary
- Configuration handling and storage
- Network communication and TLS
- The sync daemon

**Out of scope:**
- Backend service (confab-web) - report via GitHub security advisories
- Third-party dependencies - report to upstream maintainers

## Recognition

We publicly credit security researchers who report valid vulnerabilities (unless you prefer to remain anonymous). Let us know your preference when reporting.
