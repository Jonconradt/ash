#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  release_dashboard.sh --tag <tag> [--repo <owner/repo>] [--interval <seconds>] [--once]
EOF
}

tag=""
repo=""
interval=10
once=0

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
    --interval)
      interval="${2:-}"
      shift 2
      ;;
    --once)
      once=1
      shift
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

if ! [[ "$interval" =~ ^[0-9]+$ ]] || [[ "$interval" -lt 1 ]]; then
  echo "interval must be a positive integer" >&2
  exit 1
fi

find_run_id() {
  gh run list -R "$repo" --workflow release.yml --limit 20 --json databaseId,headBranch --jq ".[] | select(.headBranch == \"$tag\") | .databaseId" | head -n1 || true
}

get_run_json() {
  local run_id="$1"
  gh run view "$run_id" -R "$repo" --json databaseId,status,conclusion,displayTitle,url,jobs 2>/dev/null || true
}

get_run_field() {
  local run_json="$1"
  local field="$2"
  python3 - "$run_json" "$field" <<'PY'
import json, sys
run = json.loads(sys.argv[1])
field = sys.argv[2]
if field == "status":
    print(run.get("status", "unknown"))
elif field == "conclusion":
    print(run.get("conclusion", "n/a"))
elif field == "displayTitle":
    print(run.get("displayTitle", ""))
elif field == "url":
    print(run.get("url", ""))
else:
    print("")
PY
}

print_dashboard() {
  local run_json="$1"
  local run_status run_conclusion run_title run_url job_map release_assets_json asset_list

  run_status="$(get_run_field "$run_json" status)"
  run_conclusion="$(get_run_field "$run_json" conclusion)"
  run_title="$(get_run_field "$run_json" displayTitle)"
  run_url="$(get_run_field "$run_json" url)"

  job_map="$(python3 - "$run_json" <<'PY'
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
  printf 'Workflow run: %s\n' "$run_title"
  printf 'Workflow status: %s\n' "$run_status"
  printf 'Workflow conclusion: %s\n' "$run_conclusion"
  printf 'Workflow URL: %s\n' "$run_url"
  printf '\nJob progress:\n'
  while IFS=$'\t' read -r name status conclusion; do
    [[ -z "$name" ]] && continue
    printf '%-20s %-15s %s\n' "$name" "$status" "$conclusion"
  done <<< "$job_map"

  release_assets_json="$(gh release view "$tag" -R "$repo" --json assets --jq '.assets[].name' 2>/dev/null || true)"
  asset_list=""
  if [[ -n "$release_assets_json" ]]; then
    asset_list=$(printf '%s\n' "$release_assets_json")
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
    if printf '%s\n' "$asset_list" | grep -Fxq "$asset"; then
      state="published"
    else
      state="building"
    fi
    printf '%-38s %s\n' "$asset" "$state"
  done
}

while true; do
  run_id="$(find_run_id)"

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
    if [[ "$once" == "1" ]]; then
      exit 0
    fi
    echo
    printf 'Waiting %ss for next update...\n' "$interval"
    sleep "$interval"
    continue
  fi

  if [[ -t 1 && "$once" != "1" ]]; then
    printf '\033[2J\033[H'
  fi

  run_json="$(get_run_json "$run_id")"
  if [[ -z "$run_json" ]]; then
    echo "Workflow run $run_id exists but could not be queried" >&2
    exit 1
  fi

  print_dashboard "$run_json"

  run_status="$(get_run_field "$run_json" status)"
  if [[ "$run_status" == "completed" ]]; then
    break
  fi

  if [[ "$once" == "1" ]]; then
    break
  fi

  echo
  printf 'Waiting %ss for next update...\n' "$interval"
  sleep "$interval"
done
