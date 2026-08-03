#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  generate_repo.sh --artifact-dir <dir> --repo-dir <dir> --version <vX.Y.Z> [--suite <codename>]... [--arch <arch>]... [--origin <name>] [--label <name>] [--description <text>] [--component <name>]
EOF
}

artifact_dir=""
repo_dir=""
version=""
origin_name="ash"
label_name="ash"
description="ash apt repository"
component_name="main"
suites=()
architectures=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --artifact-dir)
      artifact_dir="${2:-}"
      shift 2
      ;;
    --repo-dir)
      repo_dir="${2:-}"
      shift 2
      ;;
    --version)
      version="${2:-}"
      shift 2
      ;;
    --suite)
      suites+=("${2:-}")
      shift 2
      ;;
    --arch)
      architectures+=("${2:-}")
      shift 2
      ;;
    --origin)
      origin_name="${2:-}"
      shift 2
      ;;
    --label)
      label_name="${2:-}"
      shift 2
      ;;
    --description)
      description="${2:-}"
      shift 2
      ;;
    --component)
      component_name="${2:-}"
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

if [[ -z "$artifact_dir" || -z "$repo_dir" || -z "$version" ]]; then
  echo "--artifact-dir, --repo-dir, and --version are required" >&2
  usage >&2
  exit 1
fi

if [[ ${#suites[@]} -eq 0 ]]; then
  suites=(jammy noble)
fi

if [[ ${#architectures[@]} -eq 0 ]]; then
  architectures=(amd64 arm64)
fi

if [[ ! -d "$artifact_dir" ]]; then
  echo "artifact directory not found: $artifact_dir" >&2
  exit 1
fi

mkdir -p "$repo_dir/pool/main/a/ash"

shopt -s nullglob
deb_files=("$artifact_dir"/*.deb)
shopt -u nullglob

if [[ ${#deb_files[@]} -eq 0 ]]; then
  echo "no .deb artifacts found in: $artifact_dir" >&2
  exit 1
fi

for deb_file in "${deb_files[@]}"; do
  install -m 0644 "$deb_file" "$repo_dir/pool/main/a/ash/$(basename "$deb_file")"
done

hash_entry() {
  local algorithm="$1"
  local file_path="$2"
  local relative_path="$3"
  local hash_value size_value
  case "$algorithm" in
    sha256)
      hash_value="$(sha256sum "$file_path" | awk '{print $1}')"
      ;;
    sha512)
      hash_value="$(sha512sum "$file_path" | awk '{print $1}')"
      ;;
    *)
      echo "unsupported hash algorithm: $algorithm" >&2
      exit 1
      ;;
  esac
  size_value="$(wc -c < "$file_path" | tr -d ' ')"
  printf ' %s %s %s\n' "$hash_value" "$size_value" "$relative_path"
}

for suite in "${suites[@]}"; do
  for architecture in "${architectures[@]}"; do
    binary_dir="$repo_dir/dists/$suite/$component_name/binary-$architecture"
    mkdir -p "$binary_dir"
    dpkg-scanpackages -a "$architecture" -m "$repo_dir/pool" /dev/null > "$binary_dir/Packages"
    gzip -9n -c "$binary_dir/Packages" > "$binary_dir/Packages.gz"
    xz -9e -T0 -c "$binary_dir/Packages" > "$binary_dir/Packages.xz"
  done

  release_file="$repo_dir/dists/$suite/Release"
  cat > "$release_file" <<EOF
Origin: $origin_name
Label: $label_name
Suite: $suite
Codename: $suite
Version: ${version#v}
Date: $(date -u +"%a, %d %b %Y %H:%M:%S UTC")
Architectures: ${architectures[*]}
Components: $component_name
Description: $description
Acquire-By-Hash: yes

EOF

  {
    echo "SHA256:"
    for architecture in "${architectures[@]}"; do
      for relative_path in "main/binary-$architecture/Packages" "main/binary-$architecture/Packages.gz" "main/binary-$architecture/Packages.xz"; do
        file_path="$repo_dir/dists/$suite/$relative_path"
        hash_entry sha256 "$file_path" "$relative_path"
      done
    done
    echo "SHA512:"
    for architecture in "${architectures[@]}"; do
      for relative_path in "main/binary-$architecture/Packages" "main/binary-$architecture/Packages.gz" "main/binary-$architecture/Packages.xz"; do
        file_path="$repo_dir/dists/$suite/$relative_path"
        hash_entry sha512 "$file_path" "$relative_path"
      done
    done
  } >> "$release_file"
done

echo "repository metadata generated in: $repo_dir"
