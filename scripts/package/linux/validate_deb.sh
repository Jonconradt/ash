#!/usr/bin/env bash
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  validate_deb.sh --pkg <path.deb> --install-path </path> --plugin-install-path </path> --license-path </path> --plugins-dir <dir> --man-path </path/name.1> --app-name <name>
EOF
}

pkg_path=""
install_path=""
man_path=""
plugin_install_path=""
license_path=""
plugins_dir=""
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
    --man-path)
      man_path="${2:-}"
      shift 2
      ;;
    --plugin-install-path)
      plugin_install_path="${2:-}"
      shift 2
      ;;
    --license-path)
      license_path="${2:-}"
      shift 2
      ;;
    --plugins-dir)
      plugins_dir="${2:-}"
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

if [[ -z "$pkg_path" || -z "$install_path" || -z "$plugin_install_path" || -z "$license_path" || -z "$plugins_dir" || -z "$man_path" || -z "$app_name" ]]; then
  echo "all arguments are required" >&2
  usage >&2
  exit 1
fi

if [[ ! -f "$pkg_path" ]]; then
  echo "package not found: $pkg_path" >&2
  exit 1
fi

if ! command -v dpkg-deb >/dev/null 2>&1; then
  echo "dpkg-deb command not found" >&2
  exit 1
fi

expected_path="${install_path%/}/$app_name"
expected_broker_path="${install_path%/}/ash-broker"
expected_man_path="${man_path%/}"
expected_license_path="${license_path#/}"
payload_list="$(dpkg-deb -c "$pkg_path" | awk '{print $6}' | sed 's#^\./##')"

if [[ -z "$payload_list" ]]; then
  echo "package payload listing is empty: $pkg_path" >&2
  exit 1
fi
for expected in "${expected_broker_path#/}" "$expected_license_path"; do
  if ! grep -Fxq "$expected" <<<"$payload_list"; then
    echo "expected payload entry missing: /$expected" >&2
    exit 1
  fi
done
while IFS= read -r plugin; do
  expected_plugin="${plugin_install_path#/}/${plugin##*/}"
  if ! grep -Fxq "$expected_plugin" <<<"$payload_list"; then
    echo "native plugin missing from package: /$expected_plugin" >&2
    exit 1
  fi
done < <(find "$plugins_dir" -maxdepth 1 -type f -perm -111 -print)

if ! grep -Eq "^${expected_path#/}$" <<<"$payload_list"; then
  echo "expected payload entry missing: $expected_path" >&2
  echo "payload entries:" >&2
  echo "$payload_list" >&2
  exit 1
fi

if ! grep -Eq "^${expected_man_path#/}$" <<<"$payload_list"; then
  echo "expected payload entry missing: $expected_man_path" >&2
  echo "payload entries:" >&2
  echo "$payload_list" >&2
  exit 1
fi
man_root="${expected_man_path%/man1/$app_name.1}"
for locale in zh_CN zh_TW es_ES ar_AE id_ID; do
  expected_localized_man="${man_root#/}/$locale/man1/$app_name.1"
  if ! grep -Fxq "$expected_localized_man" <<<"$payload_list"; then
    echo "localized man page missing from package: /$expected_localized_man" >&2
    exit 1
  fi
done

echo "package validation passed: $pkg_path"
