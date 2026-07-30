#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  build_pkg.sh --app-name <name> --version <vX.Y.Z> --binary <path> --install-path </path> --man-page <path> --man-install-path </path> --output <path.pkg>
EOF
}

app_name=""
version=""
binary_path=""
install_path=""
man_page_path=""
man_install_path=""
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
    --binary)
      binary_path="${2:-}"
      shift 2
      ;;
    --install-path)
      install_path="${2:-}"
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

if [[ -z "$app_name" || -z "$version" || -z "$binary_path" || -z "$install_path" || -z "$man_page_path" || -z "$man_install_path" || -z "$output_path" ]]; then
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

if [[ ! -x "$binary_path" ]]; then
  echo "binary not found or not executable: $binary_path" >&2
  exit 1
fi

if [[ ! -f "$man_page_path" ]]; then
  echo "man page not found: $man_page_path" >&2
  exit 1
fi

if ! command -v pkgbuild >/dev/null 2>&1; then
  echo "pkgbuild command not found (Xcode command line tools are required)" >&2
  exit 1
fi

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
mkdir -p "$target_dir"
mkdir -p "$target_man_dir"
install -m 0755 "$binary_path" "$target_dir/$app_name"
install -m 0644 "$man_page_path" "$target_man_dir/$app_name.1"

pkgbuild \
  --identifier "dev.ash.cli" \
  --version "$version" \
  --root "$payload_root" \
  --install-location "/" \
  "$output_path"

echo "created package: $output_path"
