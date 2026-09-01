# Herdr Codex Bridge

`herdr-self` gives Codex sessions their native [Herdr](https://herdr.dev/) pane
identity back when they run through a persistent centralized app-server.

Normally, Herdr injects `HERDR_*` variables directly into a Codex process. A
central app-server deliberately separates the TUI pane from the tool runner, so
those variables do not cross that boundary. The bridge joins the two identities
without patching or wrapping Codex: a trusted Codex lifecycle hook emits a
short-lived signed rendezvous marker, and a Herdr plugin binds that Codex thread
to the pane that actually displayed it.

Once associated, agents use `herdr-self` exactly like `herdr`:

```console
$ herdr-self
thread 01a05858-28e3-77d1-85ab-882a13125206
workspace w3
tab w3:t1
pane w3:p2
source herdr:codex

$ herdr-self agent list
```

All non-bridge arguments pass unchanged to the installed Herdr CLI after the
caller context is resolved and injected.

## Install

Requirements: Herdr 0.8.2 or newer and Codex CLI 0.149.0 or newer.

```sh
brew install --cask ardasevinc/tap/herdr-codex-bridge
herdr-self setup codex
herdr-self setup codex --apply
```

The first command is intentionally a dry run. `--apply` installs the pinned
Herdr plugin, replaces Herdr's built-in Codex hook with the bridge hook, installs
the small Codex skill, and creates a host-local signing key. Restart a persistent
Codex app-server afterward so it reloads the hook registry.

Go-equipped hosts can install the same tagged helper with:

```sh
go install github.com/ardasevinc/herdr-codex-bridge/cmd/herdr-self@v0.1.2
```

GitHub releases also contain four platform archives, SHA-256 checksums, SBOMs,
and keyless Sigstore bundles. Verify the signed checksum manifest before a
manual install:

```sh
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity 'https://github.com/ardasevinc/herdr-codex-bridge/.github/workflows/release.yml@refs/tags/v0.1.2' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
```

Run `herdr-self doctor` to verify the installation. To remove it, preview and
then apply `herdr-self teardown codex`; teardown restores the official Herdr
Codex integration if setup found it installed.

## Safety model

- The Herdr plugin observes only live pane output after Codex is detected.
- Markers are HMAC-authenticated, nonce-bearing, and expire after two minutes.
  This prevents accidental/model-generated claims, not malicious same-user
  processes, which already have equivalent Herdr and Codex access.
- Pane contents, marker text, signatures, and full thread IDs are never logged.
- A thread must map to exactly one live native Herdr session. Without that
  proof, mutations fail closed; a conservative set of read-only commands still
  delegates to upstream Herdr without caller context.
- There is no telemetry and no runtime network access. Network is used only for
  installation and updates.
- Codex hook trust is not bypassed. Codex may ask you to approve changed hooks.
  `herdr-self doctor` reminds you to confirm trust in `/hooks`, because Codex
  does not expose a stable noninteractive trust query.
- The prompt hook emits no output after association. While a thread is still
  unmapped, it may emit a visible signed recovery marker so the pane-side
  watcher can finish the rendezvous.

`herdr-self --skill` prints the bridge overlay followed by Herdr's live upstream
skill. `herdr-self --help` clearly delimits bridge help from upstream Herdr help.

## Development

```sh
go test ./...
go vet ./...
go build ./cmd/herdr-self
```

For a local plugin checkout, build `bin/herdr-self` and run
`herdr plugin link "$PWD"`. Do not use `setup --apply` from an unreleased `dev`
binary.

## License

Apache-2.0. See [LICENSE](LICENSE).
