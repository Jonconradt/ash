#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  sign_release.sh --gnupg-home <dir> --key-fingerprint <fingerprint> --release-file <path> [--passphrase <secret>] [--detach-output <path>] [--inrelease-output <path>]
EOF
}

gnupg_home=""
key_fingerprint=""
release_file=""
passphrase=""
detach_output=""
inrelease_output=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --gnupg-home)
      gnupg_home="${2:-}"
      shift 2
      ;;
    --key-fingerprint)
      key_fingerprint="${2:-}"
      shift 2
      ;;
    --release-file)
      release_file="${2:-}"
      shift 2
      ;;
    --passphrase)
      passphrase="${2:-}"
      shift 2
      ;;
    --detach-output)
      detach_output="${2:-}"
      shift 2
      ;;
    --inrelease-output)
      inrelease_output="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
 done

if [[ -z "$gnupg_home" || -z "$key_fingerprint" || -z "$release_file" ]]; then
  echo "--gnupg-home, --key-fingerprint, and --release-file are required" >&2
  usage >&2
  exit 1
fi

if [[ ! -f "$release_file" ]]; then
  echo "release file not found: $release_file" >&2
  exit 1
fi

mkdir -p "$gnupg_home"
chmod 700 "$gnupg_home"

release_dir="$(dirname "$release_file")"
detach_output="${detach_output:-$release_dir/Release.gpg}"
inrelease_output="${inrelease_output:-$release_dir/InRelease}"

detach_args=(--batch --yes --homedir "$gnupg_home" --local-user "$key_fingerprint")
inrelease_args=(--batch --yes --homedir "$gnupg_home" --local-user "$key_fingerprint" --armor)
if [[ -n "$passphrase" ]]; then
  detach_args+=(--pinentry-mode loopback --passphrase "$passphrase")
  inrelease_args+=(--pinentry-mode loopback --passphrase "$passphrase")
fi

gpg "${detach_args[@]}" --output "$detach_output" --detach-sign "$release_file"
gpg "${inrelease_args[@]}" --digest-algo SHA256 --output "$inrelease_output" --clearsign "$release_file"

if [[ -n "$passphrase" ]]; then
  unset passphrase
fi

echo "signed release file: $release_file"
