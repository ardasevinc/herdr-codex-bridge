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
permits its documented read-only commands but refuses mutations. A necessary
explicit mutation can use upstream `herdr` directly only with a fully specified
target. If it reports ambiguity, do not guess which pane is yours; the same
documented read-only allowance remains available without caller context.

This installed skill is complete for bridge behavior. `herdr-self --skill`
appends the live upstream reference, while `herdr-self --help` combines bridge
and upstream CLI help. `herdr-self doctor` is read-only and diagnoses bridge
setup without changing state.
