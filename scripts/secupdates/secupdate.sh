#!/usr/bin/env bash

set -Eeuo pipefail
umask 077

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
readonly REPO_ROOT
readonly LOCK_DIR="${TMPDIR:-/tmp}/ash-secupdate.lock"

log() {
	printf '[secupdate] %s\n' "$*"
}

fail() {
	printf '[secupdate] error: %s\n' "$*" >&2
	exit 1
}

release_lock() {
	rm -f -- "${LOCK_DIR}/pid"
	rmdir -- "$LOCK_DIR" 2>/dev/null || true
}

acquire_lock() {
	if mkdir -- "$LOCK_DIR" 2>/dev/null; then
		printf '%s\n' "$$" >"${LOCK_DIR}/pid"
		trap release_lock EXIT
		return
	fi

	local owner_pid
	owner_pid="$(cat "${LOCK_DIR}/pid" 2>/dev/null || true)"
	if [[ "$owner_pid" =~ ^[0-9]+$ ]] && kill -0 "$owner_pid" 2>/dev/null; then
		fail "another secupdate run is already active (pid ${owner_pid})"
	fi

	rm -f -- "${LOCK_DIR}/pid"
	rmdir -- "$LOCK_DIR" 2>/dev/null || fail "unable to remove stale lock: ${LOCK_DIR}"
	acquire_lock
}

run_privileged() {
	if [[ "$(id -u)" -eq 0 ]]; then
		"$@"
	elif command -v sudo >/dev/null 2>&1; then
		sudo -n "$@"
	else
		fail "root privileges or a noninteractive sudo installation are required for: $*"
	fi
}

update_macos() {
	command -v softwareupdate >/dev/null 2>&1 || fail "softwareupdate is not available"
	log "installing available macOS updates (Apple does not expose a reliable security-only selector)"
	run_privileged softwareupdate --install --all
}

update_linux() {
	if command -v apt-get >/dev/null 2>&1; then
		log "installing Debian/Ubuntu updates"
		run_privileged env DEBIAN_FRONTEND=noninteractive apt-get update
		command -v unattended-upgrade >/dev/null 2>&1 || fail "unattended-upgrade is required for security-only Debian/Ubuntu updates"
		run_privileged env DEBIAN_FRONTEND=noninteractive unattended-upgrade --non-interactive
	elif command -v dnf >/dev/null 2>&1; then
		log "installing Fedora/RHEL updates"
		run_privileged dnf upgrade --security --refresh --assumeyes
	elif command -v yum >/dev/null 2>&1; then
		log "installing yum updates"
		run_privileged yum update-minimal --security --assumeyes
	elif command -v zypper >/dev/null 2>&1; then
		log "installing openSUSE updates"
		run_privileged zypper --non-interactive refresh
		run_privileged zypper --non-interactive patch --category security
	elif command -v pacman >/dev/null 2>&1; then
		fail "pacman does not provide security metadata for security-only updates"
	else
		fail "no supported Linux package manager found (apt-get, dnf, yum, zypper, or pacman)"
	fi
}

update_os() {
	case "$(uname -s)" in
	Darwin)
		update_macos
		;;
	Linux)
		update_linux
		;;
	*)
		fail "unsupported operating system: $(uname -s)"
		;;
	esac
}

repository_is_clean() {
	git -C "$REPO_ROOT" diff --quiet --exit-code &&
		git -C "$REPO_ROOT" diff --cached --quiet --exit-code &&
		[[ -z "$(git -C "$REPO_ROOT" status --porcelain)" ]]
}

update_repository() {
	command -v git >/dev/null 2>&1 || fail "git is not available"
	repository_is_clean || fail "repository has local changes; commit or stash them first"
	log "updating repository"
	GIT_TERMINAL_PROMPT=0 GIT_SSH_COMMAND="ssh -o BatchMode=yes -o ConnectTimeout=15 -o ServerAliveInterval=15 -o ServerAliveCountMax=2" git -C "$REPO_ROOT" pull --ff-only
}

commit_repository_changes() {
	if repository_is_clean; then
		log "no repository changes to commit"
		return
	fi

	git -C "$REPO_ROOT" add --all
	git -C "$REPO_ROOT" diff --cached --quiet && fail "staging produced no repository changes"
	log "committing repository changes"
	git -C "$REPO_ROOT" commit --message "chore(security): apply security updates"
}

push_repository_changes() {
	local current_branch
	current_branch="$(git -C "$REPO_ROOT" symbolic-ref --quiet --short HEAD)" || fail "repository is in detached HEAD state"
	[[ -n "$current_branch" ]] || fail "repository branch name is empty"
	[[ -x "${SCRIPT_DIR}/../release/push_with_retry.sh" ]] || fail "push retry helper is not executable"
	log "pushing ${current_branch} to origin"
	"${SCRIPT_DIR}/../release/push_with_retry.sh" "pushing ${current_branch} to origin" git -C "$REPO_ROOT" push origin "$current_branch"
}

main() {
	acquire_lock
	cd -- "$REPO_ROOT"
	update_os
	update_repository
	log "running security checks"
	make security
	log "running lint and tests"
	make lint test
	if ! repository_is_clean; then
		commit_repository_changes
		push_repository_changes
	else
		log "no repository changes to commit or push"
	fi
}

main "$@"
