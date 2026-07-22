#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  validate_pkg.sh --pkg <path.pkg> --install-path </path> --app-name <name>
EOF
}

pkg_path=""
install_path=""
app_name=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --pkg)
      pkg_path="${2:-}"
      shift 2
      ;;
    --install-path)
      install_path="${2:-}"
      shift 2
      ;;
    --app-name)
      app_name="${2:-}"
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

if [[ -z "$pkg_path" || -z "$install_path" || -z "$app_name" ]]; then
  echo "all arguments are required" >&2
  usage >&2
  exit 1
fi

if [[ ! -f "$pkg_path" ]]; then
  echo "package not found: $pkg_path" >&2
  exit 1
fi

if ! command -v pkgutil >/dev/null 2>&1; then
  echo "pkgutil command not found" >&2
  exit 1
fi

expected_path="${install_path%/}/$app_name"
payload_list="$(pkgutil --payload-files "$pkg_path")"

if [[ -z "$payload_list" ]]; then
  echo "package payload listing is empty: $pkg_path" >&2
  exit 1
fi

if ! pkgutil --check-signature "$pkg_path" >/dev/null 2>&1; then
  echo "warning: package signature check could not be completed (unsigned package expected)" >&2
fi

if ! grep -Eq "^(\./)?${expected_path#/}$" <<<"$payload_list"; then
  echo "expected payload entry missing: $expected_path" >&2
  echo "payload entries:" >&2
  echo "$payload_list" >&2
  exit 1
fi

echo "package validation passed: $pkg_path"
