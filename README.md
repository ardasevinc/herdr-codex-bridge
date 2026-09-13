# Herdr Codex Bridge

`herdr-self` gives Codex sessions their native [Herdr](https://herdr.dev/) pane
identity back when they run through a persistent centralized app-server.

Normally, Herdr injects `HERDR_*` variables directly into a Codex process. A
central app-server deliberately separates the TUI pane from the tool runner, so
those variables do not cross that boundary. The bridge joins the two identities
without patching or wrapping Codex: a trusted Codex lifecycle hook emits a
signed rendezvous marker, and a Herdr plugin binds that Codex thread to the pane
that actually displayed it. If the initial marker is no longer fresh, the
pane-local watcher records a short-lived private claim and only that exact
thread may emit one fresh recovery marker for the same pane to witness.

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
the small Codex skill, and creates a host-local signing key. Existing bridge
upgrades keep the same hook command paths and normally require no app-server
restart. For a first install, verify the hooks in a fresh or resumed Codex
session. If they are absent, fully exit every connected Codex TUI before you
restart the shared app-server outside Codex; never restart it from an active
Codex turn.

Go-equipped hosts can install the same tagged helper with:

```sh
go install github.com/ardasevinc/herdr-codex-bridge/cmd/herdr-self@v0.1.7
```

GitHub releases also contain four platform archives, SHA-256 checksums, SBOMs,
and keyless Sigstore bundles. Verify the signed checksum manifest before a
manual install:

```sh
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity 'https://github.com/ardasevinc/herdr-codex-bridge/.github/workflows/release.yml@refs/tags/v0.1.7' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
```

Run `herdr-self doctor` to verify the installation. To remove it, preview and
then apply `herdr-self teardown codex`; teardown restores the official Herdr
Codex integration if setup found it installed.

## Safety model

- The Herdr plugin observes only live pane output after Codex is detected.
- Markers are HMAC-authenticated and nonce-bearing. Only a marker issued within
  two minutes may create a mapping. An older authenticated marker can identify
  a currently armed lifecycle for up to twelve hours, but can only create a
  five-minute pane-local witness claim; mapping still requires a fresh marker
  observed in that pane.
  This prevents accidental/model-generated claims, not malicious same-user
  processes, which already have equivalent Herdr and Codex access.
- Pane contents, marker text, signatures, and full thread IDs are never logged
  or persisted. Private runtime state uses HMAC-derived thread and marker tokens,
  bounded storage, `0700` directories, and `0600` files. Unwitnessed lanes
  expire fifteen minutes after their first prompt, mapped recovery arms expire
  after twelve idle hours, and opportunistic sweeps remove expired bytes without
  a resident cleanup process.
- A thread must map to exactly one live native Herdr session. Without that
  proof, mutations fail closed; a conservative set of read-only commands still
  delegates to upstream Herdr without caller context.
- There is no telemetry and no runtime network access. Network is used only for
  installation and updates.
- Codex hook trust is not bypassed. Codex may ask you to approve changed hooks.
  Because Codex has no stable noninteractive trust query, `herdr-self doctor`
  reports trust as `unknown` without treating that limitation as a failure.
- The prompt hook emits no output after association or when no Herdr pane has
  witnessed its current lifecycle marker. One visible signed recovery marker
  may be emitted after an exact live pane claim; concurrent hooks share an
  atomic at-most-once gate. Duplicate pane claims fail closed and warn once.

`herdr-self --skill` prints one authoritative bridge skill and includes Herdr's
live upstream skill as quoted reference material. The bridge preflight replaces
the upstream skill's bare `HERDR_ENV=1` precondition for centralized Codex
sessions. `herdr-self --help` clearly delimits bridge help from upstream Herdr
help and lists the exact read-only commands allowed while association is absent
or ambiguous.

`herdr-self --json` prints the current association as JSON, while
`herdr-self doctor --json` prints structured diagnostics. `--json` is not a
global flag for commands delegated to Herdr; those commands retain Herdr's own
argument contract.

### Known limitation

A replacement TUI can attach to a thread that is already resident in the
centralized app-server without receiving a new `SessionStart` marker. If the
replacement pane never witnessed the older authenticated marker, version 0.1.x
cannot safely infer that pane's thread and leaves it unmapped. Automatic
recovery remains deferred until Herdr exposes authoritative pane-occupant
identity suitable for an atomic report. Use Herdr's managed resume path when
possible instead of inferring identity from pane ID, cwd, argv, or timing.

### Manual break-glass reassociation

An operator who can see the exact target pane may submit this reserved prompt
in the Codex TUI whose current thread should own it:

```text
herdr-rebind --pane w3:p17
herdr-rebind --pane w3:p17 --replace --apply
```

The first form is a read-only preview. `--apply` makes one direct Herdr session
report using the current `UserPromptSubmit` payload's thread ID, then verifies
the target and global uniqueness. A different existing target mapping requires
`--replace`. Every recognized form blocks model execution, including malformed
or failed requests.

This is an explicitly non-atomic operator override, not automatic identity
proof. Invoke it only while personally verifying the pane and TUI. Concurrent
occupant replacement or competing writers can invalidate its prechecks and
verification. Never ask an agent to choose the target, invoke it autonomously,
retry an uncertain result, or roll it back. Every client attached to the thread
shares the resulting Herdr association.

Codex still documents deprecated custom prompts, but support differs across
builds. The matching tagged source contains
[`docs/codex-prompts/herdr-rebind.md`](docs/codex-prompts/herdr-rebind.md) as an
optional frontend for compatible clients. From that source checkout, install it
manually:

```sh
mkdir -p ~/.codex/prompts
cp docs/codex-prompts/herdr-rebind.md ~/.codex/prompts/herdr-rebind.md
```

After starting a new Codex session, preview with
`/prompts:herdr-rebind w3:p17`, then apply with
`/prompts:herdr-rebind w3:p17 --replace --apply`. If the command is unavailable,
submit the reserved plain-text form above. The bridge does not claim custom
prompt support for Codex `0.154.0`, whose implementation currently disagrees
with the published deprecated-feature documentation.

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
