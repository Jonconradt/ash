#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  validate_msi.sh --pkg <path.msi> --app-name <name>
EOF
}

pkg_path=""
app_name=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --pkg)
      pkg_path="${2:-}"
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

if [[ -z "$pkg_path" || -z "$app_name" ]]; then
  echo "all arguments are required" >&2
  usage >&2
  exit 1
fi

if [[ ! -f "$pkg_path" ]]; then
  echo "package not found: $pkg_path" >&2
  exit 1
fi

find_executable() {
  local candidate
  for candidate in "$@"; do
    if command -v "$candidate" >/dev/null 2>&1; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

msiinfo_bin="$(find_executable msiinfo msiinfo-0.20 msiinfo-0.21 msiinfo-0.22 msiinfo-0.23 || true)"
if [[ -z "$msiinfo_bin" ]]; then
  echo "msiinfo command not found. Install msitools: sudo apt-get install msitools" >&2
  exit 1
fi

if ! "$msiinfo_bin" suminfo "$pkg_path" >/dev/null 2>&1; then
  echo "package failed structural validation: $pkg_path" >&2
  exit 1
fi

file_table="$("$msiinfo_bin" export "$pkg_path" File 2>/dev/null)"
if [[ -z "$file_table" ]]; then
  echo "File table is empty in package: $pkg_path" >&2
  exit 1
fi

if ! grep -qi "${app_name}.exe" <<<"$file_table"; then
  echo "expected file not found in MSI File table: ${app_name}.exe" >&2
  echo "File table contents:" >&2
  echo "$file_table" >&2
  exit 1
fi

echo "package validation passed: $pkg_path"
