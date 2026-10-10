#!/bin/sh
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

set -eu

repository="Jonconradt/ash"
install_dir=${ASH_INSTALL_DIR:-"$HOME/.local/bin"}

fail() {
  printf 'ash installer: %s\n' "$1" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Install the latest ash release for Linux, macOS, or FreeBSD.

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

shell_name=$(basename "${SHELL:-}")
case $shell_name in
  bash|zsh|fish) install_shell=$shell_name ;;
  *) fail "unsupported shell: ${shell_name:-unknown} (supported shells: bash, zsh, fish)" ;;
esac

command -v tar >/dev/null 2>&1 || fail "tar is required"
command -v uname >/dev/null 2>&1 || fail "uname is required"
command -v install >/dev/null 2>&1 || fail "install is required"
command -v cosign >/dev/null 2>&1 || fail "cosign is required to verify release signatures; install Sigstore cosign and rerun"

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
  FreeBSD) goos=freebsd ;;
  Linux) goos=linux ;;
  *) fail "unsupported operating system: $os" ;;
esac

data_home=${XDG_DATA_HOME:-"$HOME/.local/share"}
case $data_home in
  /*) ;;
  *) fail "XDG_DATA_HOME must be an absolute path: $data_home" ;;
esac

machine=$(uname -m)
case $machine in
  x86_64|amd64) goarch=amd64 ;;
  arm64|aarch64) goarch=arm64 ;;
  *) fail "unsupported architecture: $machine (ash releases target amd64 and arm64; request yours at https://github.com/$repository/issues/new)" ;;
esac

if [ "$goos" = freebsd ] && ! command -v bash >/dev/null 2>&1; then
  fail "bash is required on FreeBSD; install it with: pkg install bash"
fi

command -v sha256sum >/dev/null 2>&1 && checksum_command=sha256sum
if [ -z "${checksum_command:-}" ] && command -v shasum >/dev/null 2>&1; then
  checksum_command=shasum
fi
if [ -z "${checksum_command:-}" ] && command -v sha256 >/dev/null 2>&1; then
  checksum_command=sha256
fi
[ -n "${checksum_command:-}" ] || fail "sha256sum, shasum, or sha256 is required"

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
download "$download_base/SHA256SUMS.sigstore.json" "$tmp_dir/SHA256SUMS.sigstore.json" || fail "could not download signature bundle"

cosign verify-blob "$tmp_dir/SHA256SUMS" \
  --bundle "$tmp_dir/SHA256SUMS.sigstore.json" \
  --certificate-identity "https://github.com/$repository/.github/workflows/release.yml@refs/tags/$tag" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  >/dev/null || fail "release signature verification failed"

expected=$(awk -v name="$asset" '$2 == name { print $1; exit }' "$tmp_dir/SHA256SUMS")
[ -n "$expected" ] || fail "SHA256SUMS does not contain $asset"
case $checksum_command in
  sha256sum) actual=$(sha256sum "$tmp_dir/$asset" | awk '{print $1}') ;;
  shasum) actual=$(shasum -a 256 "$tmp_dir/$asset" | awk '{print $1}') ;;
  sha256) actual=$(sha256 -q "$tmp_dir/$asset") ;;
esac
[ "$actual" = "$expected" ] || fail "checksum verification failed for $asset"

mkdir -p "$install_dir" || fail "could not create installation directory $install_dir"
tar -xzf "$tmp_dir/$asset" -C "$tmp_dir" || fail "could not extract $asset"
[ -f "$tmp_dir/$base" ] || fail "release archive did not contain $base"
install -m 0755 "$tmp_dir/$base" "$install_dir/ash" 2>/dev/null || {
  cp "$tmp_dir/$base" "$install_dir/ash" || fail "could not install ash to $install_dir"
  chmod 0755 "$install_dir/ash" || fail "could not make ash executable"
}

[ -f "$tmp_dir/ash-broker" ] || fail "release archive did not contain ash-broker"
install -m 0755 "$tmp_dir/ash-broker" "$install_dir/ash-broker" 2>/dev/null || {
  cp "$tmp_dir/ash-broker" "$install_dir/ash-broker" || fail "could not install ash-broker to $install_dir"
  chmod 0755 "$install_dir/ash-broker" || fail "could not make ash-broker executable"
}

plugins_dir="$data_home/ash/plugins"
mkdir -p "$plugins_dir" || fail "could not create plugins directory $plugins_dir"
chmod 700 "$plugins_dir" 2>/dev/null || true
if [ -d "$tmp_dir/plugins" ]; then
  for plugin_source in "$tmp_dir/plugins"/*; do
    [ -f "$plugin_source" ] || continue
    plugin_name=$(basename "$plugin_source")
    case $plugin_name in
      ''|.*|*/*) fail "release archive contained invalid plugin name: $plugin_name" ;;
    esac
    install -m 0755 "$plugin_source" "$plugins_dir/$plugin_name" 2>/dev/null || {
      cp "$plugin_source" "$plugins_dir/$plugin_name" || fail "could not install plugin $plugin_name"
      chmod 0755 "$plugins_dir/$plugin_name" || fail "could not make plugin $plugin_name executable"
    }
  done
else
  printf 'ash installer: %s does not ship plugins; native plugins will be unavailable\n' "$tag" >&2
fi

# When this script itself is piped (curl ... | sh), stdin is the script body, not the user's
# terminal, so interactive prompts must read from /dev/tty instead. The device node can exist
# and still be unopenable when there is no controlling terminal, so probe it by opening it.
if { : < /dev/tty; } 2>/dev/null; then
  have_tty=yes
else
  have_tty=no
fi

# 'ash install' builds a virtualenv for the bundled Python tools, which needs the venv module
# and ensurepip. Several platforms ship those separately from the base python3, so offer to
# install them here rather than letting venv creation fail with a cryptic ensurepip error.
ensure_python_venv() {
  command -v python3 >/dev/null 2>&1 || return 0
  python3 -c 'import ensurepip, venv' >/dev/null 2>&1 && return 0

  printf '\nash bundles optional Python tools that need the python3 venv module and ensurepip.\n' >&2
  printf 'Your python3 does not provide them.\n' >&2
  printf 'ash works without them; only the bundled Python tools are disabled.\n' >&2

  venv_package=''
  venv_install_cmd=''
  venv_privileged=no
  case $goos in
    linux)
      if command -v apt-get >/dev/null 2>&1; then
        venv_package=python3-venv
        venv_install_cmd='apt-get install -y python3-venv'
        venv_privileged=yes
      fi
      ;;
    freebsd)
      # FreeBSD ships pip (and the bundled wheels ensurepip needs) as a versioned pyXY-pip package.
      if command -v pkg >/dev/null 2>&1; then
        python_abi=$(python3 -c 'import sys; print("py%d%d" % sys.version_info[:2])' 2>/dev/null) || python_abi=''
        if [ -n "$python_abi" ]; then
          venv_package="$python_abi-pip"
          venv_install_cmd="pkg install -y $python_abi-pip"
          venv_privileged=yes
        fi
      fi
      ;;
    darwin)
      # Homebrew's python includes venv and ensurepip, and must never be run under sudo.
      if command -v brew >/dev/null 2>&1; then
        venv_package=python
        venv_install_cmd='brew install python'
        venv_privileged=no
      fi
      ;;
  esac

  if [ -z "$venv_install_cmd" ]; then
    printf "Install your platform's python3 venv/pip package, then rerun: ash install\n\n" >&2
    return 0
  fi

  if [ "$venv_privileged" = yes ] && [ "$(id -u)" != 0 ]; then
    if ! command -v sudo >/dev/null 2>&1; then
      printf 'Install it as root with: %s\nThen rerun: ash install\n\n' "$venv_install_cmd" >&2
      return 0
    fi
    venv_install_cmd="sudo $venv_install_cmd"
  fi

  if [ "$have_tty" != yes ]; then
    printf 'No terminal is available to prompt you.\n' >&2
    printf 'Install it with: %s\nThen rerun: ash install\n\n' "$venv_install_cmd" >&2
    return 0
  fi

  printf 'Install %s now (%s)? [y/N] ' "$venv_package" "$venv_install_cmd" >&2
  answer=''
  read -r answer < /dev/tty || answer=''
  case $answer in
    y|Y|yes|Yes|YES) ;;
    *)
      printf 'Skipping. Install it later with: %s\n\n' "$venv_install_cmd" >&2
      return 0
      ;;
  esac

  # The install can fail on a bad sudo password, a user outside sudoers, or a missing
  # package; none of that is fatal to installing ash itself.
  # shellcheck disable=SC2024 # the /dev/tty redirect feeds the package manager's stdin, not sudo's
  if $venv_install_cmd < /dev/tty >&2; then
    if python3 -c 'import ensurepip, venv' >/dev/null 2>&1; then
      printf 'python3 venv module is now available.\n\n' >&2
    else
      printf '%s installed but the venv module is still unavailable; continuing without bundled Python tools.\n\n' "$venv_package" >&2
    fi
  else
    printf 'Could not install %s; continuing without bundled Python tools.\n\n' "$venv_package" >&2
  fi
  return 0
}

ensure_python_venv

shell_integration=no
if [ "$have_tty" = yes ]; then
  printf 'Enable %s shell integration by editing its startup file? [y/N] ' "$install_shell" >&2
  answer=''
  read -r answer < /dev/tty || answer=''
  case $answer in
    y|Y|yes|Yes|YES) shell_integration=yes ;;
  esac
fi

if [ "$shell_integration" = yes ]; then
  "$install_dir/ash" install --shell "$install_shell" --shell-integration < /dev/tty
else
  "$install_dir/ash" install --shell "$install_shell"
fi

printf 'ash %s installed to %s/ash\n' "$tag" "$install_dir"
if [ "$shell_integration" != yes ]; then
  printf 'Shell integration was not enabled. Run: ash install --shell %s --shell-integration\n' "$install_shell"
fi
printf 'Open a new shell to use the installed ash-broker; existing shell sessions keep their running broker.\n'
case :$PATH: in
  *:"$install_dir":*) ;;
  *) printf 'Open a new shell to pick up %s on PATH.\n' "$install_dir" ;;
esac