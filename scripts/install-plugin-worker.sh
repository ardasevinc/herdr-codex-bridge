#!/bin/sh
set -eu

version="$(awk -F '"' '/^version = / { print $2; exit }' herdr-plugin.toml)"
case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *) printf '%s\n' "unsupported operating system: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  arm64|aarch64) arch="arm64" ;;
  x86_64|amd64) arch="amd64" ;;
  *) printf '%s\n' "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

archive="herdr-codex-bridge_${version}_${os}_${arch}.tar.gz"
base_url="https://github.com/ardasevinc/herdr-codex-bridge/releases/download/v${version}"
temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/herdr-codex-bridge.XXXXXX")"
trap 'rm -rf "$temp_dir"' EXIT HUP INT TERM

curl --fail --location --silent --show-error "$base_url/$archive" --output "$temp_dir/$archive"
curl --fail --location --silent --show-error "$base_url/checksums.txt" --output "$temp_dir/checksums.txt"
if command -v cosign >/dev/null 2>&1; then
  curl --fail --location --silent --show-error "$base_url/checksums.txt.sigstore.json" --output "$temp_dir/checksums.txt.sigstore.json"
  cosign verify-blob \
    --bundle "$temp_dir/checksums.txt.sigstore.json" \
    --certificate-identity "https://github.com/ardasevinc/herdr-codex-bridge/.github/workflows/release.yml@refs/tags/v${version}" \
    --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
    "$temp_dir/checksums.txt" >/dev/null
fi
expected="$(awk -v name="$archive" '$2 == name { print $1; exit }' "$temp_dir/checksums.txt")"
[ -n "$expected" ] || { printf '%s\n' "checksum missing for $archive" >&2; exit 1; }
if command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$temp_dir/$archive" | awk '{print $1}')"
else
  actual="$(sha256sum "$temp_dir/$archive" | awk '{print $1}')"
fi
[ "$actual" = "$expected" ] || { printf '%s\n' "checksum mismatch for $archive" >&2; exit 1; }

mkdir -p bin
tar -xzf "$temp_dir/$archive" -C "$temp_dir"
install -m 0755 "$temp_dir/herdr-self" bin/herdr-self
