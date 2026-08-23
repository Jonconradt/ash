#!/bin/sh
set -eu

repository="Jonconradt/ash"
install_dir=${ASH_INSTALL_DIR:-"$HOME/.local/bin"}

fail() {
  printf 'ash installer: %s\n' "$1" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Install the latest ash release for Linux or macOS.

Usage: install.sh

Set ASH_INSTALL_DIR to choose another installation directory.
EOF
}

case ${1:-} in
  -h|--help)
    usage
    exit 0
    ;;
  '')
    ;;
  *)
    fail "unknown argument: $1"
    ;;
esac

command -v tar >/dev/null 2>&1 || fail "tar is required"
command -v uname >/dev/null 2>&1 || fail "uname is required"
command -v install >/dev/null 2>&1 || fail "install is required"

if command -v curl >/dev/null 2>&1; then
  downloader=curl
elif command -v wget >/dev/null 2>&1; then
  downloader=wget
elif command -v python3 >/dev/null 2>&1; then
  downloader=python3
elif command -v python >/dev/null 2>&1; then
  downloader=python
else
  fail "curl, wget, or python is required"
fi

download() {
  url=$1
  destination=$2
  case $downloader in
    curl) curl -fsSL "$url" -o "$destination" ;;
    wget) wget -qO "$destination" "$url" ;;
    python3|python)
      "$downloader" - "$url" "$destination" <<'PY'
import sys
import urllib.request

url, destination = sys.argv[1:]
request = urllib.request.Request(url, headers={"User-Agent": "ash-installer/1"})
with urllib.request.urlopen(request) as response, open(destination, "wb") as output:
    output.write(response.read())
PY
      ;;
  esac
}

os=$(uname -s)
case $os in
  Darwin) goos=darwin ;;
  Linux) goos=linux ;;
  *) fail "unsupported operating system: $os" ;;
esac

machine=$(uname -m)
case $machine in
  x86_64|amd64) goarch=amd64 ;;
  arm64|aarch64) goarch=arm64 ;;
  *) fail "unsupported architecture: $machine" ;;
esac

command -v sha256sum >/dev/null 2>&1 && checksum_command=sha256sum
if [ -z "${checksum_command:-}" ] && command -v shasum >/dev/null 2>&1; then
  checksum_command=shasum
fi
[ -n "${checksum_command:-}" ] || fail "sha256sum or shasum is required"

tmp_dir=$(mktemp -d 2>/dev/null || mktemp -d -t ash-install)
cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT HUP INT TERM

download "https://api.github.com/repos/$repository/releases/latest" "$tmp_dir/release.json" || fail "could not find the latest release"
tag=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$tmp_dir/release.json" | head -n 1)
[ -n "$tag" ] || fail "latest release did not contain a tag"

asset="ash-${tag}-${goos}-${goarch}.tar.gz"
base=${asset%.tar.gz}
download_base="https://github.com/$repository/releases/download/$tag"

download "$download_base/$asset" "$tmp_dir/$asset" || fail "could not download $asset"
download "$download_base/SHA256SUMS" "$tmp_dir/SHA256SUMS" || fail "could not download SHA256SUMS"

expected=$(awk -v name="$asset" '$2 == name { print $1; exit }' "$tmp_dir/SHA256SUMS")
[ -n "$expected" ] || fail "SHA256SUMS does not contain $asset"
case $checksum_command in
  sha256sum) actual=$(sha256sum "$tmp_dir/$asset" | awk '{print $1}') ;;
  shasum) actual=$(shasum -a 256 "$tmp_dir/$asset" | awk '{print $1}') ;;
esac
[ "$actual" = "$expected" ] || fail "checksum verification failed for $asset"

mkdir -p "$install_dir" || fail "could not create installation directory $install_dir"
tar -xzf "$tmp_dir/$asset" -C "$tmp_dir" || fail "could not extract $asset"
[ -f "$tmp_dir/$base" ] || fail "release archive did not contain $base"
install -m 0755 "$tmp_dir/$base" "$install_dir/ash" 2>/dev/null || {
  cp "$tmp_dir/$base" "$install_dir/ash" || fail "could not install ash to $install_dir"
  chmod 0755 "$install_dir/ash" || fail "could not make ash executable"
}

shell_name=$(basename "${SHELL:-}")
case $shell_name in
  zsh) install_shell=zsh ;;
  *) install_shell=bash ;;
esac

"$install_dir/ash" install --shell "$install_shell"

printf 'ash %s installed to %s/ash\n' "$tag" "$install_dir"
case :$PATH: in
  *:"$install_dir":*) ;;
  *) printf 'Add %s to PATH before using the ash command.\n' "$install_dir" ;;
esac