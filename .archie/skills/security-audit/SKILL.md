---
name: security-audit
description: >
  Scan code for security vulnerabilities: hardcoded secrets, injection vectors,
  unsafe deserialization, missing auth checks, and path traversal. Use on PRs
  touching auth, API endpoints, or data handling code.
version: 1.0.0
metadata:
  archie:
    tools: [golangci-lint, trivy, gitleaks]
    engine: any
---
# Security Audit

## When to use

Run this audit when:
- A PR touches authentication, authorization, or session code
- New API endpoints are added
- Database queries or file I/O paths change
- The issue mentions "security", "vulnerability", "CVE", or "auth"
- A dependency update changes major versions

## What to check

### 1. Hardcoded secrets
Scan for:
- API keys, tokens, passwords in source files
- `.env` files committed to the repo
- Credentials in test fixtures

Use `gitleaks detect --no-git` for a fast scan.

### 2. Injection vectors
- **SQL:** any string concatenation in query building. Use parameterized queries.
- **Command:** any `os/exec` with user-controlled input. Use `exec.Command` with separate args, never `sh -c`.
- **Path:** any file path constructed from user input without validation. Use `filepath.Clean` and check the resolved path is within the expected directory.

### 3. Unsafe deserialization
- `json.Unmarshal` on untrusted input into `interface{}` or `map[string]any` without validation
- `yaml.Unmarshal` without `KnownFields` or strict mode
- `gob.Decode` on network input

### 4. Missing auth checks
- New endpoints without middleware
- Internal/admin endpoints exposed without auth
- Token validation that only checks signature, not expiry or revocation

### 5. Dependency CVEs
Run `trivy fs --severity HIGH,CRITICAL .` on the repository.
Report each finding with:
- Package name and version
- CVE ID
- CVSS score
- Fixed version (if available)

## Reporting

For each finding, report:
1. **Severity:** Critical / High / Medium / Low
2. **Location:** file, line, function
3. **Description:** what's wrong
4. **Fix:** specific code change needed
5. **Prevention:** how to catch this in future (lint rule, gate addition)

If nothing is found, report clean. Never fabricate findings.
