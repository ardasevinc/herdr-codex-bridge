# Herdr Codex Bridge commands

## Inspect

```sh
herdr-self
herdr-self --json
herdr-self doctor --json
herdr-self docs agents
herdr-self docs commands --json
```

`herdr-self` resolves the current `CODEX_THREAD_ID` to exactly one native Herdr
pane. Document and help commands are offline. Doctor inspects installation and
mapping state without changing it.

## Reassociate an unmapped thread

```sh
herdr-self rebind --pane w3:p17
herdr-self rebind --pane w3:p17 --replace
herdr-self rebind --pane w3:p17 --replace --apply
```

Preview first. The first form previews an unmapped target. Add `--replace` to
preview replacement when the target contains a different mapping. `--apply`
rechecks current state, attempts at most one report, and verifies the observed
result. It does not guarantee mutation if transport or verification is
uncertain, and it never retries or rolls back.

Rebind reads only the invoking tool process's `CODEX_THREAD_ID`. By default it
uses the canonical Herdr socket and ignores inherited `HERDR_SOCKET_PATH`; an
explicit placement-independent `--socket PATH` selects another server. Rebind
cannot move a thread already mapped elsewhere or override duplicate mappings.

## Install or remove

```sh
herdr-self setup codex
herdr-self setup codex --apply
herdr-self teardown codex
herdr-self teardown codex --apply
```

Setup and teardown are previews unless `--apply` is present. Use `--force` only
to replace locally modified bridge-managed files. No bridge command restarts the
shared Codex app-server automatically.
