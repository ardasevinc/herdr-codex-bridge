# Contributing

Run `go test ./...`, `go vet ./...`, and `test -z "$(gofmt -l .)"` before opening
a pull request. Keep the bridge Codex-specific and dependency-free unless a new
dependency has a concrete, reviewed operational benefit.

Protocol changes must remain backward-aware: the package version follows SemVer,
while the signed marker protocol has its own explicit version.
