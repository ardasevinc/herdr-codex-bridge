# Security Policy

Please report security issues privately through GitHub Security Advisories for
this repository. Do not open a public issue for a vulnerability.

The bridge handles a host-local HMAC key and terminal output. It must never log
the key, signatures, marker text, pane contents, or full Codex thread IDs during
normal operation. `herdr-self doctor --json` may include an explicitly requested
full mapping, but never the signing material.

## Threat boundary

The signed marker prevents accidental or model-generated terminal text from
claiming a Codex session. It is not a security boundary against another process
running as the same operating-system user. Such a process can read the local
key and already has equivalent access to the Herdr socket and Codex state. The
nonce provides marker uniqueness; the timestamp and watcher-generation check
bound stale-output reuse. They are not presented as replay protection against a
malicious same-user process.

Release archives and checksums are signed with keyless Sigstore bundles. The
macOS Homebrew cask removes quarantine from the staged unsigned CLI binary so
it can run without an Apple Developer ID. Users who require Apple notarization
should not install version 0.1.x; that distribution property is not implied by
the Sigstore signature.
