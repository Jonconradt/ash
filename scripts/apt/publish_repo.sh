#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  publish_repo.sh --repo-dir <dir> --origin-url <url> [--branch gh-pages] [--commit-message <text>]
EOF
}

repo_dir=""
origin_url=""
branch_name="gh-pages"
commit_message="Publish apt repository"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-dir)
      repo_dir="${2:-}"
      shift 2
      ;;
    --origin-url)
      origin_url="${2:-}"
      shift 2
      ;;
    --branch)
      branch_name="${2:-}"
      shift 2
      ;;
    --commit-message)
      commit_message="${2:-}"
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

if [[ -z "$repo_dir" || -z "$origin_url" ]]; then
  echo "--repo-dir and --origin-url are required" >&2
  usage >&2
  exit 1
fi

if [[ ! -d "$repo_dir" ]]; then
  echo "repository directory not found: $repo_dir" >&2
  exit 1
fi

publish_dir="$(mktemp -d)"
cleanup() {
  rm -rf "$publish_dir"
}
trap cleanup EXIT

git init "$publish_dir" >/dev/null
git -C "$publish_dir" config user.name "github-actions[bot]"
git -C "$publish_dir" config user.email "github-actions[bot]@users.noreply.github.com"
git -C "$publish_dir" remote add origin "$origin_url"

if git ls-remote --exit-code --heads origin "$branch_name" >/dev/null 2>&1; then
  git -C "$publish_dir" fetch --depth 1 origin "$branch_name"
  git -C "$publish_dir" checkout -B "$branch_name" "origin/$branch_name" >/dev/null
else
  git -C "$publish_dir" checkout --orphan "$branch_name" >/dev/null
fi

if git -C "$publish_dir" rev-parse --verify HEAD >/dev/null 2>&1; then
  git -C "$publish_dir" rm -rf . >/dev/null 2>&1 || true
fi
find "$publish_dir" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +
cp -R "$repo_dir"/. "$publish_dir"/
touch "$publish_dir/.nojekyll"

git -C "$publish_dir" add -A

if git -C "$publish_dir" diff --cached --quiet; then
  echo "no apt repository changes to publish"
  exit 0
fi

git -C "$publish_dir" commit -m "$commit_message" >/dev/null
git -C "$publish_dir" push origin "$branch_name"

echo "published apt repository to branch: $branch_name"
