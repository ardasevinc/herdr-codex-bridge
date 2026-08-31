# Security Policy

Please report security issues privately through GitHub Security Advisories for
this repository. Do not open a public issue for a vulnerability.

The bridge handles a host-local HMAC key and terminal output. It must never log
the key, signatures, marker text, pane contents, or full Codex thread IDs during
normal operation. `herdr-self doctor --json` may include an explicitly requested
full mapping, but never the signing material.
