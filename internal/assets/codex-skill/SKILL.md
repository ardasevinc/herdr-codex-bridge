---
name: herdr-self
description: Use Herdr safely from Codex, including centralized app-server sessions where HERDR_ENV is intentionally absent.
---

# Herdr Codex Bridge

Use `herdr-self` whenever an action is relative to your own Herdr pane. It resolves
your Codex thread to Herdr's native live-pane association, injects the normal
`HERDR_*` context, then delegates unchanged arguments to the installed `herdr` CLI.

This skill is the authoritative adaptation for centralized Codex sessions. Its
preflight supersedes the bare upstream Herdr rule that requires `HERDR_ENV=1` in
the calling tool process. For caller-relative examples in the quoted upstream
reference, use `herdr-self` in place of `herdr`; the bridge injects the proven
pane context into the delegated command.

`HERDR_ENV` being unset is normal when Codex uses a centralized app-server. Do not
panic or refuse all Herdr work solely for that reason. Run `herdr-self` with no
arguments to inspect your association. If it reports pending, `herdr-self` still
permits its documented read-only commands but refuses delegated mutations. The
bridge-owned `rebind` escape hatch below is the explicit exception. A necessary
other mutation can use upstream `herdr` directly only with a fully specified
target. If it reports ambiguity, do not guess which pane is yours; the same
documented read-only allowance remains available without caller context.

This installed skill is complete for bridge behavior. `herdr-self --skill`
appends the live upstream reference, while `herdr-self --help` combines bridge
and upstream CLI help. `herdr-self doctor` is read-only and diagnoses bridge
setup without changing state. `herdr-self docs agents` reads these exact embedded
instructions offline. `herdr-self docs commands --json` returns versioned command
and effect metadata without requiring identity, configuration, or Herdr.

## Break-glass reassociation

When this Codex thread is unmapped and specific live Herdr evidence identifies
its intended pane, an agent may use
`herdr-self rebind --pane <fully-qualified-pane-id>`. `--replace` permits
replacing a different mapping at that target; it does not move this thread from
another pane or override duplicate mappings. The command uses the invoking tool
process's `CODEX_THREAD_ID`; it never accepts a thread override and ignores
inherited `HERDR_*` identity.

Always preview first. If the target still has a different mapping, inspect that
state and then use `--replace --apply` only when the evidence supports replacing
it. Do not choose a pane from cwd, argv, terminal title, focus, or timing alone.
Do not retry or roll back an uncertain result. This escape hatch is explicitly
non-atomic: `--replace` records a judgment about the observed mapping, not proof
that the pane's occupant could not change during the operation.
