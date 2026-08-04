#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  release_dashboard.sh --tag <tag> [--repo <owner/repo>]
EOF
}

tag=""
repo=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)
      tag="${2:-}"
      shift 2
      ;;
    --repo)
      repo="${2:-}"
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

if [[ -z "$tag" ]]; then
  tag="$(git describe --tags --exact-match 2>/dev/null || true)"
fi

if [[ -z "$tag" ]]; then
  echo "no release tag provided" >&2
  exit 1
fi

if [[ -z "$repo" ]]; then
  repo="$(gh repo view --json nameWithOwner --jq .nameWithOwner 2>/dev/null || true)"
fi

if [[ -z "$repo" ]]; then
  echo "unable to resolve repository; set --repo owner/name" >&2
  exit 1
fi

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI is required" >&2
  exit 1
fi

run_id="$(gh run list -R "$repo" --workflow release.yml --limit 20 --json databaseId,headBranch --jq ".[] | select(.headBranch == \"$tag\") | .databaseId" | head -n1 || true)"

if [[ -z "$run_id" ]]; then
  echo "No release workflow run found for tag $tag in $repo"
  echo "Status: pending"
  echo
  echo "Expected release deliverables:"
  for asset in \
    "ash-${tag}-darwin-amd64.pkg" \
    "ash-${tag}-darwin-arm64.pkg" \
    "ash-${tag}-darwin-amd64.tar.gz" \
    "ash-${tag}-darwin-arm64.tar.gz" \
    "ash-${tag}-linux-amd64.deb" \
    "ash-${tag}-linux-arm64.deb" \
    "ash-${tag}-linux-amd64.rpm" \
    "ash-${tag}-linux-arm64.rpm" \
    "ash-${tag}-linux-amd64.tar.gz" \
    "ash-${tag}-linux-arm64.tar.gz" \
    "ash-${tag}-windows-amd64.msi" \
    "ash-${tag}-windows-arm64.msi"; do
    printf '%-38s %s\n' "$asset" "pending"
  done
  exit 0
fi

run_json="$(gh run view "$run_id" -R "$repo" --json databaseId,status,conclusion,displayTitle,url,jobs 2>/dev/null || true)"
if [[ -z "$run_json" ]]; then
  echo "Workflow run $run_id exists but could not be queried" >&2
  exit 1
fi

job_map="$(python3 - <<'PY' "$run_json"
import json, sys
run = json.loads(sys.argv[1])
jobs = run.get('jobs', [])
for job in jobs:
    name = job.get('name', '')
    status = job.get('status', '')
    conclusion = job.get('conclusion', '')
    print(f"{name}\t{status}\t{conclusion}")
PY
)"

printf 'Release dashboard for %s\n' "$tag"
printf 'Workflow run: %s\n' "$(python3 - <<'PY' "$run_json"
import json, sys
run = json.loads(sys.argv[1])
print(run.get('displayTitle', ''))
PY
)"
printf 'Workflow status: %s\n' "$(python3 - <<'PY' "$run_json"
import json, sys
run = json.loads(sys.argv[1])
print(run.get('status', 'unknown'))
PY
)"
printf 'Workflow conclusion: %s\n' "$(python3 - <<'PY' "$run_json"
import json, sys
run = json.loads(sys.argv[1])
print(run.get('conclusion', 'n/a'))
PY
)"
printf 'Workflow URL: %s\n' "$(python3 - <<'PY' "$run_json"
import json, sys
run = json.loads(sys.argv[1])
print(run.get('url', ''))
PY
)"
printf '\nJob progress:\n'
while IFS=$'\t' read -r name status conclusion; do
  [[ -z "$name" ]] && continue
  printf '%-20s %-15s %s\n' "$name" "$status" "$conclusion"
done <<< "$job_map"

release_assets_json="$(gh release view "$tag" -R "$repo" --json assets --jq '.assets[].name' 2>/dev/null || true)"
asset_list=""
if [[ -n "$release_assets_json" ]]; then
  asset_list=$(printf '%s
' "$release_assets_json")
fi

printf '\nDeliverable progress:\n'
for asset in \
  "ash-${tag}-darwin-amd64.pkg" \
  "ash-${tag}-darwin-arm64.pkg" \
  "ash-${tag}-darwin-amd64.tar.gz" \
  "ash-${tag}-darwin-arm64.tar.gz" \
  "ash-${tag}-linux-amd64.deb" \
  "ash-${tag}-linux-arm64.deb" \
  "ash-${tag}-linux-amd64.rpm" \
  "ash-${tag}-linux-arm64.rpm" \
  "ash-${tag}-linux-amd64.tar.gz" \
  "ash-${tag}-linux-arm64.tar.gz" \
  "ash-${tag}-windows-amd64.msi" \
  "ash-${tag}-windows-arm64.msi"; do
  if printf '%s
' "$asset_list" | grep -Fxq "$asset"; then
    state="published"
  else
    state="building"
  fi
  printf '%-38s %s\n' "$asset" "$state"
done
