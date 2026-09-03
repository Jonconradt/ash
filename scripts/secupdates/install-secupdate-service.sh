#!/usr/bin/env bash
# Install and configure the ash security-update service for a developer machine.
#
# Linux (systemd): installs ash-secupdate.service + .timer, the OnFailure mail
#   unit, the failure-mail helper, and a NOPASSWD sudoers fragment so the
#   service can apt-get update / unattended-upgrade noninteractively.
# macOS (launchd): installs a LaunchAgent that runs secupdate.sh daily.
#   `softwareupdate --install --all` still requires sudo, so on macOS the agent
#   runs the repo/security-check portion and OS updates stay interactive unless
#   you run the agent as root (not recommended).
#
# Usage:
#   install-secupdate-service.sh [options]
# Options:
#   --repo PATH      Path to the ash checkout        (default: dir two levels up)
#   --user NAME      User the service runs as         (default: current user)
#   --mailto ADDR    Failure-mail recipient           (default: <user>@<domain>; see below)
#   --bin-dir PATH   Install dir for the mail helper  (default: ~/bin)
#   --no-sudoers     Skip writing the sudoers fragment (Linux)
#   --uninstall      Remove everything this script installed
#   -h|--help        Show this help
#
# The default recipient is derived from the service user and the machine's
# domain (hostname minus the first label), e.g. user "jon" on host
# "n7oob.example.com" -> "jon@example.com". A single-label hostname has no
# derivable domain, so pass --mailto explicitly in that case.
#
# The failure-mail helper only sends mail when msmtp is configured; without a
# working /usr/bin/msmtp the failure unit fails harmlessly and the journal still
# records the failure.
set -Eeuo pipefail
umask 022

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR

log() { printf '[install-secupdate] %s\n' "$*"; }
fail() { printf '[install-secupdate] error: %s\n' "$*" >&2; exit 1; }

REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
SERVICE_USER="$(id -un)"
MAILTO=""
BIN_DIR=""
WRITE_SUDOERS=1
DO_UNINSTALL=0

while [[ $# -gt 0 ]]; do
	case "$1" in
	--repo) REPO_ROOT="${2:?--repo needs a value}"; shift 2 ;;
	--user) SERVICE_USER="${2:?--user needs a value}"; shift 2 ;;
	--mailto) MAILTO="${2:?--mailto needs a value}"; shift 2 ;;
	--bin-dir) BIN_DIR="${2:?--bin-dir needs a value}"; shift 2 ;;
	--no-sudoers) WRITE_SUDOERS=0; shift ;;
	--uninstall) DO_UNINSTALL=1; shift ;;
	-h|--help) sed -n '2,30p' "${BASH_SOURCE[0]}"; exit 0 ;;
	*) fail "unknown option: $1 (use --help)" ;;
	esac
done

# Derive a default recipient "$SERVICE_USER@<domain>" where <domain> is the
# hostname with its first label stripped (n7oob.example.com -> example.com).
default_mailto() {
	local fqdn domain
	fqdn="$(hostname -f 2>/dev/null || hostname 2>/dev/null || true)"
	domain="${fqdn#*.}"
	if [[ -z "$fqdn" || "$domain" == "$fqdn" || -z "$domain" ]]; then
		return 1 # single-label hostname: no domain to derive
	fi
	printf '%s@%s\n' "$SERVICE_USER" "$domain"
}

if [[ -z "$MAILTO" ]]; then
	if ! MAILTO="$(default_mailto)"; then
		fail "could not derive a default mailto from hostname '$(hostname -f 2>/dev/null || hostname)'; pass --mailto explicitly"
	fi
	log "no --mailto given; derived recipient ${MAILTO}"
fi

REPO_ROOT="$(cd -- "$REPO_ROOT" 2>/dev/null && pwd)" || fail "repo path not found: $REPO_ROOT"
[[ -x "${REPO_ROOT}/scripts/secupdates/secupdate.sh" ]] || fail "secupdate.sh not found/executable under ${REPO_ROOT}"
[[ -f "${REPO_ROOT}/scripts/secupdates/secupdate-failure-mail.sh" ]] || fail "secupdate-failure-mail.sh template missing under ${REPO_ROOT}"

USER_HOME="$(getent passwd "$SERVICE_USER" 2>/dev/null | cut -d: -f6)"
USER_HOME="${USER_HOME:-$HOME}"
[[ -n "$BIN_DIR" ]] || BIN_DIR="${USER_HOME}/bin"

OS="$(uname -s)"

write_file() { # path mode owner content-via-stdin
	local path="$1" mode="$2" owner="$3"
	if [[ "$(id -u)" -eq 0 ]]; then
		install -m "$mode" -o "${owner%%:*}" -g "${owner##*:}" /dev/stdin "$path"
	else
		sudo install -m "$mode" -o "${owner%%:*}" -g "${owner##*:}" /dev/stdin "$path"
	fi
}

# --- sudoers (Linux) ---------------------------------------------------------
# sudoers NOPASSWD rules match argv literally (full path + args), so resolve the
# binaries now and emit full-path rules. unattended-upgrade takes NO trailing
# arg (the installed version has no --non-interactive flag).
install_sudoers() {
	[[ "$WRITE_SUDOERS" -eq 1 ]] || { log "skipping sudoers (--no-sudoers)"; return; }
	local apt uu
	apt="$(command -v apt-get)" || fail "apt-get not found; sudoers fragment is apt-specific"
	uu="$(command -v unattended-upgrade)" || fail "unattended-upgrade not found"
	local tmp
	tmp="$(mktemp)"
	cat >"$tmp" <<EOF
# ash-secupdate: allow the service to apply security updates noninteractively.
${SERVICE_USER} ALL=(root) NOPASSWD: /usr/bin/env DEBIAN_FRONTEND=noninteractive ${apt} update
${SERVICE_USER} ALL=(root) NOPASSWD: /usr/bin/env DEBIAN_FRONTEND=noninteractive ${uu}
EOF
	sudo visudo -cf "$tmp" >/dev/null || { rm -f "$tmp"; fail "generated sudoers fragment failed visudo validation"; }
	write_file /etc/sudoers.d/ash-secupdate 0440 root:root <"$tmp"
	rm -f "$tmp"
	log "installed /etc/sudoers.d/ash-secupdate"
}

# --- failure-mail helper ------------------------------------------------------
install_mail_helper() {
	mkdir -p "$BIN_DIR"
	# Escape sed replacement metacharacters (&, \, |) in the recipient.
	local esc
	esc="$(printf '%s\n' "$MAILTO" | sed 's/[&\\|]/\\&/g')"
	sed "s/__MAILTO__/${esc}/g" \
		"${REPO_ROOT}/scripts/secupdates/secupdate-failure-mail.sh" \
		| install -m 0755 /dev/stdin "${BIN_DIR}/ash-secupdate-failure-mail"
	log "installed ${BIN_DIR}/ash-secupdate-failure-mail (mailto=${MAILTO})"
}

# --- Linux / systemd ----------------------------------------------------------
install_linux() {
	command -v systemctl >/dev/null 2>&1 || fail "systemd not found"
	local svc_path="/etc/systemd/system/ash-secupdate.service"
	local tmr_path="/etc/systemd/system/ash-secupdate.timer"
	local fail_path="/etc/systemd/system/ash-secupdate-failure.service"

	write_file "$svc_path" 0644 root:root <<EOF
[Unit]
Description=Apply Ubuntu security updates and validate ash
Wants=network-online.target
After=network-online.target
OnFailure=ash-secupdate-failure.service

[Service]
Type=oneshot
User=${SERVICE_USER}
Group=${SERVICE_USER}
WorkingDirectory=${REPO_ROOT}
Environment=HOME=${USER_HOME}
Environment=PATH=${USER_HOME}/.local/bin:/usr/local/bin:/usr/bin:/bin:/snap/bin:/usr/local/go/bin:${USER_HOME}/go/bin
ExecStart=${REPO_ROOT}/scripts/secupdates/secupdate.sh
UMask=0077
EOF

	write_file "$tmr_path" 0644 root:root <<'EOF'
[Unit]
Description=Daily ash security update

[Timer]
OnCalendar=*-*-* 03:30:00
RandomizedDelaySec=30m
Persistent=true
Unit=ash-secupdate.service

[Install]
WantedBy=timers.target
EOF

	write_file "$fail_path" 0644 root:root <<EOF
[Unit]
Description=Email ash security update failures
After=network-online.target

[Service]
Type=oneshot
User=${SERVICE_USER}
Group=${SERVICE_USER}
Environment=HOME=${USER_HOME}
Environment=PATH=${USER_HOME}/.local/bin:/usr/local/bin:/usr/bin:/bin
ExecStart=${BIN_DIR}/ash-secupdate-failure-mail
TimeoutStartSec=60
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
PrivateTmp=true
EOF

	install_sudoers
	install_mail_helper

	sudo systemctl daemon-reload
	sudo systemctl enable --now ash-secupdate.timer
	log "enabled ash-secupdate.timer; verify with: systemctl list-timers ash-secupdate.timer"
}

# --- macOS / launchd ----------------------------------------------------------
install_macos() {
	local label="com.ash.secupdate"
	local plist="${USER_HOME}/Library/LaunchAgents/${label}.plist"
	mkdir -p "${USER_HOME}/Library/LaunchAgents" "${USER_HOME}/Library/Logs"
	cat >"$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>${label}</string>
	<key>ProgramArguments</key>
	<array><string>${REPO_ROOT}/scripts/secupdates/secupdate.sh</string></array>
	<key>WorkingDirectory</key><string>${REPO_ROOT}</string>
	<key>StartCalendarInterval</key>
	<dict><key>Hour</key><integer>3</integer><key>Minute</key><integer>30</integer></dict>
	<key>StandardOutPath</key><string>${USER_HOME}/Library/Logs/ash-secupdate.log</string>
	<key>StandardErrorPath</key><string>${USER_HOME}/Library/Logs/ash-secupdate.log</string>
	<key>EnvironmentVariables</key>
	<dict><key>PATH</key><string>${USER_HOME}/.local/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin</string></dict>
</dict>
</plist>
EOF
	launchctl bootout "gui/$(id -u)/${label}" 2>/dev/null || true
	launchctl bootstrap "gui/$(id -u)" "$plist"
	log "loaded ${label}; logs at ${USER_HOME}/Library/Logs/ash-secupdate.log"
	log "note: macOS 'softwareupdate --install --all' needs sudo; run interactively or grant rights."
}

uninstall() {
	case "$OS" in
	Linux)
		sudo systemctl disable --now ash-secupdate.timer 2>/dev/null || true
		sudo rm -f /etc/systemd/system/ash-secupdate.service \
			/etc/systemd/system/ash-secupdate.timer \
			/etc/systemd/system/ash-secupdate-failure.service
		[[ "$WRITE_SUDOERS" -eq 1 ]] && sudo rm -f /etc/sudoers.d/ash-secupdate
		sudo systemctl daemon-reload
		rm -f "${BIN_DIR}/ash-secupdate-failure-mail"
		log "removed systemd units${WRITE_SUDOERS:+ and sudoers} and mail helper"
		;;
	Darwin)
		launchctl bootout "gui/$(id -u)/com.ash.secupdate" 2>/dev/null || true
		rm -f "${USER_HOME}/Library/LaunchAgents/com.ash.secupdate.plist"
		log "removed launchd agent"
		;;
	esac
}

main() {
	if [[ "$DO_UNINSTALL" -eq 1 ]]; then uninstall; exit 0; fi
	log "repo=${REPO_ROOT} user=${SERVICE_USER} mailto=${MAILTO} os=${OS}"
	case "$OS" in
	Linux) install_linux ;;
	Darwin) install_macos ;;
	*) fail "unsupported OS: $OS" ;;
	esac
	log "done. Test with the service's normal trigger, then check the journal/log."
}

main "$@"
