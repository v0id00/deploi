# Security Policy

## Supported Versions

Only the **latest release** receives security fixes. We don't maintain an
LTS line — always upgrade to the newest version.

| Version     | Supported          |
|-------------|--------------------|
| latest (≥ v0.1.0) | ✅           |
| older       | ❌                 |

## Reporting a Vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report them privately via **GitHub Private Vulnerability Reporting**:

1. Go to https://github.com/v0id00/deploi/security/advisories
2. Click **New draft security advisory**
3. Fill in the details — include affected version, a minimal reproduction,
   and (if known) the impact.

Alternatively, email **berkay.gun.00@gmail.com** with the subject
`[deploi security] ...` and encrypt sensitive details if needed.

### What happens next

- You'll get an acknowledgment within **48 hours**.
- We'll confirm the issue and its severity within **7 days**.
- A fix ships in the next release; a security advisory is published after
  the fix is available, giving credit to the reporter unless they prefer
  anonymity.

## Scope

In scope: the `deploi` CLI itself — remote command execution via hooks,
server credentials in `deploi.toml`, and the transfer (rsync/SFTP) code
paths.

Out of scope: the SSH servers you deploy to, and anything in your own
`deploi.toml` (keep that file out of version control — it's in
`.gitignore`).
