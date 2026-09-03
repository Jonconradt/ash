#!/usr/bin/env bash
# Emails the ash-secupdate failure journal. Deployed host-only to
# /home/jon/bin/ash-secupdate-failure-mail and wired to ash-secupdate.service via
# OnFailure=ash-secupdate-failure.service. This repo copy is the source of truth;
# reinstall to the host after editing.
set -Eeuo pipefail

host="$(hostname -f 2>/dev/null || hostname)"

# A Go panic floods the journal with thousands of goroutine-dump lines, so a
# plain `journalctl -n` keeps only the stack tail and drops the actual error.
# Filter to the failure signature + step markers, then byte-cap (not line-cap)
# the tail so the panic header and the failing [secupdate] step survive.
journal="$(
	journalctl -u ash-secupdate.service --since "-1h" --no-pager 2>&1 \
		| grep -Ei '\[secupdate\]|panic:|fatal|FAIL|Error|error:|exit status|vulnerabilit|govulncheck|Vulnerability' \
		| tail -c 20000
)"

{
	printf "To: __MAILTO__\n"
	printf "Subject: ash security update failed on %s\n" "$host"
	printf "Content-Type: text/plain; charset=UTF-8\n\n"
	printf "The ash security update service failed on %s.\n\n" "$host"
	printf "%s\n" "$journal"
} | /usr/bin/msmtp -t
