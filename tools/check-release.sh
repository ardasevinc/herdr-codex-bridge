#!/bin/sh
set -eu

manifest_version="$(awk -F '"' '/^version = / { print $2; exit }' herdr-plugin.toml)"
[ -n "$manifest_version" ] || { printf '%s\n' 'missing plugin version' >&2; exit 1; }
grep -Fq 'binary: herdr-self' .goreleaser.yaml
grep -Fq 'homebrew_casks:' .goreleaser.yaml
grep -Fq 'name: herdr-codex-bridge' .goreleaser.yaml
grep -Fq 'min_herdr_version = "0.8.2"' herdr-plugin.toml
grep -Fq 'go 1.24.0' go.mod

case "${GITHUB_REF_TYPE:-}" in
  tag)
    [ "${GITHUB_REF_NAME#v}" = "$manifest_version" ] || {
      printf '%s\n' "tag ${GITHUB_REF_NAME} does not match plugin version ${manifest_version}" >&2
      exit 1
    }
    ;;
esac
