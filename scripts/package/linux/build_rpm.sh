#!/usr/bin/env bash
# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  build_rpm.sh --app-name <name> --version <vX.Y.Z> --arch <amd64|arm64> --binary <path> --broker <path> --plugins-dir <dir> --license-file <path> --third-party-notices-file <path> --install-path </path> --plugin-install-path </path> --license-install-path </path> --man-page <path> --man-install-path </path> [--localized-man-dir <dir>] --output <path.rpm>
EOF
}

app_name=""
version=""
arch=""
binary_path=""
broker_path=""
plugins_dir=""
license_file=""
third_party_notices_file=""
install_path=""
plugin_install_path=""
license_install_path=""
man_page_path=""
man_install_path=""
localized_man_dir=""
output_path=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --app-name)
      app_name="${2:-}"
      shift 2
      ;;
    --version)
      version="${2:-}"
      shift 2
      ;;
    --arch)
      arch="${2:-}"
      shift 2
      ;;
    --binary)
      binary_path="${2:-}"
      shift 2
      ;;
    --broker)
      broker_path="${2:-}"
      shift 2
      ;;
    --plugins-dir)
      plugins_dir="${2:-}"
      shift 2
      ;;
    --license-file)
      license_file="${2:-}"
      shift 2
      ;;
    --third-party-notices-file)
      third_party_notices_file="${2:-}"
      shift 2
      ;;
    --install-path)
      install_path="${2:-}"
      shift 2
      ;;
    --plugin-install-path)
      plugin_install_path="${2:-}"
      shift 2
      ;;
    --license-install-path)
      license_install_path="${2:-}"
      shift 2
      ;;
    --man-page)
      man_page_path="${2:-}"
      shift 2
      ;;
    --man-install-path)
      man_install_path="${2:-}"
      shift 2
      ;;
    --localized-man-dir)
      localized_man_dir="${2:-}"
      shift 2
      ;;
    --output)
      output_path="${2:-}"
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

if [[ -z "$app_name" || -z "$version" || -z "$arch" || -z "$binary_path" || -z "$broker_path" || -z "$plugins_dir" || -z "$license_file" || -z "$third_party_notices_file" || -z "$install_path" || -z "$plugin_install_path" || -z "$license_install_path" || -z "$man_page_path" || -z "$man_install_path" || -z "$output_path" ]]; then
  echo "all arguments are required" >&2
  usage >&2
  exit 1
fi

if [[ "$install_path" != /* ]]; then
  echo "install path must be absolute: $install_path" >&2
  exit 1
fi

if [[ "$man_install_path" != /* ]]; then
  echo "man install path must be absolute: $man_install_path" >&2
  exit 1
fi
if [[ "$plugin_install_path" != /* || "$license_install_path" != /* ]]; then
  echo "plugin and license install paths must be absolute" >&2
  exit 1
fi

for executable in "$binary_path" "$broker_path"; do
  if [[ ! -x "$executable" ]]; then
    echo "binary not found or not executable: $executable" >&2
    exit 1
  fi
done

if [[ ! -f "$license_file" ]]; then
  echo "license file not found: $license_file" >&2
  exit 1
fi
if [[ ! -f "$third_party_notices_file" ]]; then
  echo "third-party notices file not found: $third_party_notices_file" >&2
  exit 1
fi

if [[ ! -d "$plugins_dir" ]] || [[ -z "$(find "$plugins_dir" -maxdepth 1 -type f -perm -111 -print -quit)" ]]; then
  echo "executable plugins not found in: $plugins_dir" >&2
  exit 1
fi

if [[ ! -f "$man_page_path" ]]; then
  echo "man page not found: $man_page_path" >&2
  exit 1
fi

if ! command -v fpm >/dev/null 2>&1; then
  echo "fpm command not found. Install with: gem install --no-document --version 1.18.0 fpm" >&2
  exit 1
fi

rpm_arch=""
case "$arch" in
  amd64) rpm_arch="x86_64" ;;
  arm64) rpm_arch="aarch64" ;;
  *)
    echo "unsupported architecture: $arch (expected amd64 or arm64)" >&2
    exit 1
    ;;
esac

output_dir="$(dirname "$output_path")"
mkdir -p "$output_dir"

stage_dir="$(mktemp -d)"
cleanup() {
  rm -rf "$stage_dir"
}
trap cleanup EXIT

payload_root="$stage_dir/root"
target_dir="$payload_root$install_path"
target_man_dir="$payload_root$man_install_path"
target_plugin_dir="$payload_root$plugin_install_path"
mkdir -p "$target_dir"
mkdir -p "$target_man_dir"
mkdir -p "$target_plugin_dir" "$(dirname "$payload_root$license_install_path")"
install -m 0755 "$binary_path" "$target_dir/$app_name"
install -m 0755 "$broker_path" "$target_dir/ash-broker"
find "$plugins_dir" -maxdepth 1 -type f -perm -111 -exec install -m 0755 {} "$target_plugin_dir/" \;
install -m 0644 "$license_file" "$payload_root$license_install_path"
install -m 0644 "$third_party_notices_file" "$(dirname "$payload_root$license_install_path")/THIRD_PARTY_NOTICES.md"
install -m 0644 "$man_page_path" "$target_man_dir/$app_name.1"
if [[ -n "$localized_man_dir" ]]; then
  if [[ ! -d "$localized_man_dir" ]]; then
    echo "localized man page directory not found: $localized_man_dir" >&2
    exit 1
  fi
  man_root="${man_install_path%/man1}"
  if [[ "$man_root" == "$man_install_path" ]]; then
    echo "man install path must end in /man1 to install localized pages: $man_install_path" >&2
    exit 1
  fi
  localized_count=0
  for page in "$localized_man_dir"/*/"$app_name".1; do
    [[ -f "$page" ]] || continue
    locale="$(basename "$(dirname "$page")")"
    locale_man_dir="$payload_root$man_root/$locale/man1"
    mkdir -p "$locale_man_dir"
    install -m 0644 "$page" "$locale_man_dir/$app_name.1"
    ((localized_count += 1))
  done
  if [[ "$localized_count" -eq 0 ]]; then
    echo "no localized man pages found in: $localized_man_dir" >&2
    exit 1
  fi
fi

clean_version="${version#v}"
clean_version="${clean_version/-beta./~beta.}"
clean_version="${clean_version/-nightly./~nightly.}"

fpm -s dir -t rpm \
  -n "$app_name" \
  -v "$clean_version" \
  --architecture "$rpm_arch" \
  --prefix / \
  -C "$payload_root" \
  -p "$output_path" \
  --description "ash shell assistant" \
  --url "https://github.com" \
  .

echo "created package: $output_path"
