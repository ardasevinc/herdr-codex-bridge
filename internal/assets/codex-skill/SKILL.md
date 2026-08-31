---
name: herdr-self
description: Use Herdr safely from Codex, including centralized app-server sessions where HERDR_ENV is intentionally absent.
---

# Herdr Codex Bridge

Use `herdr-self` whenever an action is relative to your own Herdr pane. It resolves
your Codex thread to Herdr's native live-pane association, injects the normal
`HERDR_*` context, then delegates unchanged arguments to the installed `herdr` CLI.

`HERDR_ENV` being unset is normal when Codex uses a centralized app-server. Do not
panic or refuse all Herdr work solely for that reason. Run `herdr-self` with no
arguments to inspect your association. If it reports pending, `herdr-self` still
permits its documented read-only commands but refuses mutations. A necessary
explicit mutation can use upstream `herdr` directly only with a fully specified
target. If it reports ambiguity, do not guess which pane is yours.

For Herdr's complete live instructions, run `herdr-self --skill`. For combined
bridge and upstream CLI help, run `herdr-self --help`. `herdr-self doctor` is
read-only and diagnoses bridge setup without changing state.
