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

if ! [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]]; then
  echo "tag must look like vX.Y.Z (optionally with suffix), got: $tag" >&2
  exit 1
fi

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI is required" >&2
  exit 1
fi

if [[ -z "$repo" ]]; then
  repo="$(gh repo view --json nameWithOwner --jq .nameWithOwner 2>/dev/null || true)"
fi

if [[ -z "$repo" ]]; then
  echo "unable to resolve repository; set --repo owner/name" >&2
  exit 1
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required" >&2
  exit 1
fi

if ! [[ "$interval" =~ ^[0-9]+$ ]] || [[ "$interval" -lt 1 ]]; then
  echo "interval must be a positive integer" >&2
  exit 1
fi

spinner_frames='|/-\'
spinner_index=0
use_tui=0
if [[ -t 1 && "$once" != "1" ]]; then
  use_tui=1
fi

expected_assets=()
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
  "ash-${tag}-windows-amd64.msi"; do
  expected_assets+=("$asset")
done

cleanup_terminal() {
  if [[ "$use_tui" == "1" ]]; then
    printf '\033[0m\033[?25h\033[?1049l'
  fi
}

enter_tui() {
  if [[ "$use_tui" == "1" ]]; then
    printf '\033[?1049h\033[?25l'
  fi
}

find_run_id() {
  gh run list -R "$repo" --workflow release.yml --limit 20 --json databaseId,headBranch --jq ".[] | select(.headBranch == \"$tag\") | .databaseId" | head -n1 || true
}

get_run_json() {
  local run_id="$1"
  gh run view "$run_id" -R "$repo" --json databaseId,status,conclusion,displayTitle,url,jobs 2>/dev/null || true
}

get_release_assets_json() {
  gh release view "$tag" -R "$repo" --json assets 2>/dev/null || true
}

get_failure_summary() {
  local run_json="$1"

  python3 - "$tag" "$run_json" <<'PY'
import json
import sys

tag = sys.argv[1]
run_json = sys.argv[2]


def parse_json(raw: str):
  raw = raw.strip()
  if not raw:
    return None
  return json.loads(raw)


def first_failed_step(job: dict) -> str:
  steps = job.get("steps") or []
  for step in steps:
    conclusion = (step.get("conclusion") or "").lower()
    if conclusion and conclusion != "success" and conclusion != "skipped":
      step_name = step.get("name") or "unnamed step"
      return f"{step_name} ({conclusion})"
  return "no failed step details available"


run = parse_json(run_json)
if not isinstance(run, dict):
  print(f"Release {tag} failed, but no workflow details were available.")
  sys.exit(0)

jobs = run.get("jobs", []) or []
failed_jobs = []
for job in jobs:
  status = (job.get("status") or "").lower()
  conclusion = (job.get("conclusion") or "").lower()
  if status == "completed" and conclusion and conclusion != "success":
    failed_jobs.append(job)

if not failed_jobs:
  conclusion = run.get("conclusion") or "unknown"
  print(f"Release {tag} workflow finished with conclusion={conclusion}.")
  sys.exit(0)

workflow_conclusion = run.get("conclusion") or "unknown"
workflow_url = run.get("url") or ""
print(f"Release {tag} failed with conclusion={workflow_conclusion}.")
if workflow_url:
  print(f"Workflow URL: {workflow_url}")
print("Failed jobs:")
for job in failed_jobs:
  job_name = job.get("name") or "unnamed job"
  job_conclusion = job.get("conclusion") or "unknown"
  job_url = job.get("url") or ""
  detail = first_failed_step(job)
  if job_url:
    print(f"- {job_name}: {job_conclusion}; first failed step: {detail}; job URL: {job_url}")
  else:
    print(f"- {job_name}: {job_conclusion}; first failed step: {detail}")
PY
}

get_release_progress() {
  local run_json="$1"
  local assets_json="$2"

  python3 - "$run_json" "$assets_json" "${expected_assets[@]}" <<'PY'
import json
import sys

run_json = sys.argv[1]
assets_json = sys.argv[2]
expected_assets = sys.argv[3:]


def parse_json(raw: str):
  raw = raw.strip()
  if not raw:
    return None
  return json.loads(raw)


run = parse_json(run_json)
assets_doc = parse_json(assets_json)
published_assets = set()
if isinstance(assets_doc, dict):
  for asset in assets_doc.get("assets", []):
    name = asset.get("name")
    if name:
      published_assets.add(name)

assets_completed = sum(1 for name in expected_assets if name in published_assets)
assets_total = len(expected_assets)
workflow_status = ""
workflow_conclusion = ""
if isinstance(run, dict):
  workflow_status = (run.get("status") or "").lower()
  workflow_conclusion = (run.get("conclusion") or "").lower()

release_complete = assets_total > 0 and assets_completed == assets_total
workflow_complete = workflow_status == "completed"

if release_complete:
  print("complete")
  print("published")
elif workflow_complete and workflow_conclusion and workflow_conclusion != "success":
  print("complete")
  print("workflow_failed")
elif workflow_complete:
  print("pending")
  print("awaiting_assets")
else:
  print("pending")
  print("active")
PY
}

render_dashboard() {
  local mode="$1"
  local run_json="$2"
  local assets_json="$3"
  local spinner="${spinner_frames:spinner_index:1}"

  python3 - "$mode" "$tag" "$repo" "$interval" "$spinner" "$run_json" "$assets_json" "${expected_assets[@]}" <<'PY'
import json
import shutil
import sys
from collections import Counter

mode = sys.argv[1]
tag = sys.argv[2]
repo = sys.argv[3]
interval = sys.argv[4]
spinner = sys.argv[5]
run_json = sys.argv[6]
assets_json = sys.argv[7]
expected_assets = sys.argv[8:]

CSI = "\033["
RESET = f"{CSI}0m"
DIM = f"{CSI}2m"
BOLD = f"{CSI}1m"
COLORS = {
    "green": f"{CSI}32m",
    "yellow": f"{CSI}33m",
    "red": f"{CSI}31m",
    "blue": f"{CSI}34m",
    "cyan": f"{CSI}36m",
}


def colorize(text: str, color: str) -> str:
    if mode != "tui":
        return text
    return f"{COLORS[color]}{text}{RESET}"


def style(text: str, token: str) -> str:
    if mode != "tui":
        return text
    return f"{token}{text}{RESET}"


def status_label(status: str, conclusion: str) -> tuple[str, str]:
    status = (status or "unknown").lower()
    conclusion = (conclusion or "").lower()
    if status == "completed":
        if conclusion == "success":
            return "PASS", "green"
        if conclusion in {"failure", "startup_failure", "timed_out", "cancelled", "action_required"}:
            return conclusion.upper(), "red"
        return (conclusion or "DONE").upper(), "yellow"
    if status in {"in_progress", "queued", "waiting", "requested", "pending"}:
        return status.replace("_", " ").upper(), "yellow"
    return status.replace("_", " ").upper(), "blue"


def asset_state(name: str, published: set[str], workflow_status: str) -> tuple[str, str]:
    if name in published:
        return "PUBLISHED", "green"
    if workflow_status == "completed":
        return "MISSING", "red"
    return "BUILDING", "yellow"


def bar(completed: int, total: int, width: int) -> str:
    if total <= 0:
        total = 1
    filled = max(0, min(width, round(width * completed / total)))
    return "[" + ("#" * filled) + ("-" * (width - filled)) + "]"


def truncate(text: str, width: int) -> str:
    if width <= 0:
        return ""
    if len(text) <= width:
        return text
    if width <= 3:
        return text[:width]
    return text[: width - 3] + "..."


def parse_json(raw: str):
    raw = raw.strip()
    if not raw:
        return None
    return json.loads(raw)


run = parse_json(run_json)
assets_doc = parse_json(assets_json)
published_assets = set()
if isinstance(assets_doc, dict):
    for asset in assets_doc.get("assets", []):
        name = asset.get("name")
        if name:
            published_assets.add(name)

jobs = []
workflow_status = "pending"
workflow_conclusion = ""
workflow_title = f"release {tag}"
workflow_url = ""
run_id = "pending"

if isinstance(run, dict):
    jobs = run.get("jobs", []) or []
    workflow_status = run.get("status") or "unknown"
    workflow_conclusion = run.get("conclusion") or ""
    workflow_title = run.get("displayTitle") or workflow_title
    workflow_url = run.get("url") or ""
    run_id = str(run.get("databaseId") or "unknown")

job_counts = Counter()
for job in jobs:
    label, _ = status_label(job.get("status", ""), job.get("conclusion", ""))
    job_counts[label] += 1

assets_completed = sum(1 for name in expected_assets if name in published_assets)
assets_total = len(expected_assets)
jobs_completed = sum(1 for job in jobs if (job.get("status") or "").lower() == "completed")
jobs_total = len(jobs)
summary_completed = assets_completed + jobs_completed
summary_total = assets_total + max(jobs_total, 1)
term_width = shutil.get_terminal_size((110, 40)).columns
bar_width = min(36, max(18, term_width - 72))
name_width = min(34, max(22, term_width - 68))

workflow_label, workflow_color = status_label(workflow_status, workflow_conclusion)
heading = f"Release Control Center :: {tag}"
if run is None:
    heading = f"Release Control Center :: {tag} :: waiting for workflow"

lines = []
if mode == "tui":
    lines.append(f"{CSI}2J{CSI}H")

lines.append(style(heading, BOLD))
lines.append("=" * min(len(heading), max(40, term_width - 2)))
lines.append(
    f"Repo: {repo}    Run: {run_id}    State: {colorize(workflow_label, workflow_color)}    Refresh: {interval}s"
)
lines.append(
    f"Progress: {colorize(bar(summary_completed, summary_total, bar_width), 'cyan')} {summary_completed}/{summary_total} checkpoints"
)

if run is None:
    lines.append(f"Pulse: {colorize(spinner, 'yellow')} waiting for release workflow to appear for tag {tag}")
else:
    finished = workflow_status == "completed"
    pulse_text = "steady" if finished else f"{spinner} live"
    pulse_color = "green" if finished and workflow_conclusion == "success" else "yellow"
    lines.append(
        f"Pulse: {colorize(pulse_text, pulse_color)}    Jobs: {jobs_completed}/{jobs_total or 0} complete    Assets: {assets_completed}/{assets_total} published"
    )

lines.append("")
lines.append(style("Workflow", BOLD))
lines.append(f"Title: {workflow_title}")
lines.append(f"URL:   {workflow_url or 'not available yet'}")

if jobs:
    counts_summary = ", ".join(f"{label}:{count}" for label, count in sorted(job_counts.items()))
    lines.append(f"Mix:   {counts_summary}")
else:
    lines.append("Mix:   no jobs reported yet")

lines.append("")
lines.append(style("Jobs", BOLD))
if jobs:
    for job in jobs:
        name = job.get("name") or "unnamed job"
        started = job.get("startedAt") or ""
        completed = job.get("completedAt") or ""
        label, color = status_label(job.get("status", ""), job.get("conclusion", ""))
        timing = completed or started or "pending"
        lines.append(
            f"- {truncate(name, name_width):<{name_width}} {colorize(label, color):<24} {truncate(timing, 26)}"
        )
else:
    lines.append("- Workflow has not reported jobs yet")

lines.append("")
lines.append(style("Deliverables", BOLD))
for name in expected_assets:
    state, color = asset_state(name, published_assets, workflow_status)
    lines.append(f"- {truncate(name, 46):<46} {colorize(state, color)}")

lines.append("")
if workflow_status == "completed":
    if workflow_conclusion == "success" and assets_completed == assets_total:
        lines.append(colorize("Release finished cleanly. All expected artifacts are published.", "green"))
    elif workflow_conclusion == "success":
        lines.append(colorize("Workflow finished, but some expected artifacts are still missing from the release.", "red"))
    else:
        lines.append(colorize("Workflow finished with a non-success conclusion. Check the run URL for the failing job.", "red"))
else:
    lines.append(style("Watching for the next refresh without growing the terminal buffer.", DIM))

sys.stdout.write("\n".join(lines))
if not lines[-1].endswith("\n"):
    sys.stdout.write("\n")
PY
}

trap cleanup_terminal EXIT
enter_tui

final_message=""

while true; do
  run_id="$(find_run_id)"
  run_json=""
  if [[ -n "$run_id" ]]; then
    run_json="$(get_run_json "$run_id")"
    if [[ -z "$run_json" ]]; then
      echo "Workflow run $run_id exists but could not be queried" >&2
      exit 1
    fi
  fi

  assets_json="$(get_release_assets_json)"

  if [[ "$use_tui" == "1" ]]; then
    render_dashboard tui "$run_json" "$assets_json"
  else
    render_dashboard plain "$run_json" "$assets_json"
  fi

  mapfile -t release_progress < <(get_release_progress "$run_json" "$assets_json")
  release_state="${release_progress[0]:-pending}"

  if [[ "$release_state" == "complete" ]]; then
    case "${release_progress[1]:-published}" in
      published)
        final_message="Release $tag is complete. All expected artifacts are published."
        ;;
      workflow_failed)
        final_message="$(get_failure_summary "$run_json")"
        ;;
    esac
    break
  fi

  if [[ "$once" == "1" ]]; then
    break
  fi

  spinner_index=$(((spinner_index + 1) % 4))
  sleep "$interval"
done

if [[ "$use_tui" == "1" ]]; then
  cleanup_terminal
  trap - EXIT
fi

if [[ -n "$final_message" ]]; then
  echo "$final_message"
fi
