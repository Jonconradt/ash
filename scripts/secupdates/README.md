# secupdates

Automated, scheduled application of OS security updates, followed by a full
re-validation of the ash codebase. Designed to run unattended under a systemd
timer (Linux) or launchd agent (macOS), and to email a diagnosis when anything
fails.

## Purpose

The pipeline answers one question every night: *"Is the system patched, and does
ash still pass its security/lint/test gates against the latest code?"*

A run does four things, in order:

1. Apply OS security updates (`apt-get` + `unattended-upgrade` on Debian/Ubuntu,
   `dnf`/`yum`/`zypper` elsewhere, `softwareupdate` on macOS).
2. `git pull --ff-only` the repo (must be clean — local changes abort the run).
3. Run `make security` (gosec + govulncheck) and `make lint test`.
4. Commit and push any resulting changes.

### What would ever be committed?

**In practice, nothing.** Every command in step 3 is read-only: `gosec` and
`govulncheck` are pure scanners that report but never modify, and the lint chain
(`golangci-lint run`, `ruff check`, `ruff format --check`, `yamlfmt -lint`,
`markdownlint-cli2`) runs in *check* mode — none of them edit files, and
`go test` doesn't touch the tree. A successful run leaves the repo clean, hits
the "no repository changes to commit or push" branch, and pushes nothing.

The commit/push step is **insurance**, and exists for two reasons:

1. **Future auto-fixing targets.** If a `make` target ever starts *writing*
   instead of checking — a `gofmt -w`, an import organizer, a codegen step, a
   version-pin bump that rewrites a lockfile — the reconciled file becomes the
   commit instead of leaving the host's tree dirty.
2. **Keeping tomorrow's pull green.** Step 2's `git pull --ff-only` requires a
   clean repo. Committing-and-pushing a legitimate auto-fix is the escape hatch
   that prevents a dirty tree from wedging every subsequent nightly run.

So if this step ever fires, it means a check began modifying the tree. If you'd
rather it *never* auto-commit and instead treat post-run dirt as a reportable
failure, make the branch in `secupdate.sh` strict.

## Scripts

| Script | Role | Where it runs |
| --- | --- | --- |
| [secupdate.sh](secupdate.sh) | The pipeline itself. This is the `ExecStart` of the service. | On the host, triggered by the timer/agent. |
| [secupdate-failure-mail.sh](secupdate-failure-mail.sh) | Source of truth for the failure-mail helper. Installed host-side (with `--mailto` substituted for its `__MAILTO__` placeholder) and wired to `OnFailure=`; emails the captured journal on failure. | Installed to `<bin-dir>/ash-secupdate-failure-mail` (default `~/bin`). |
| [install-secupdate-service.sh](install-secupdate-service.sh) | Installs/configures the whole thing for a developer. Generates systemd units + sudoers (Linux) or a launchd agent (macOS). | Run once per machine, locally. |

## Usage

### Install / configure a machine

```bash
# Defaults: current user, repo = two levels up,
#           mailto = <user>@<machine-domain> (see below)
./scripts/secupdates/install-secupdate-service.sh

# Customized
./scripts/secupdates/install-secupdate-service.sh \
  --repo /home/you/ash --user you --mailto you@example.com

# Remove everything it installed
./scripts/secupdates/install-secupdate-service.sh --uninstall
```

The default recipient is the service user (`--user`, default the current user)
at the machine's domain — `hostname -f` with its first label stripped. For user
`jon` on host `n7oob.example.com`, the default is `jon@example.com`. A
single-label hostname (e.g. just `n7oob`) has no derivable domain, so the
installer stops and asks you to pass `--mailto` explicitly. Override any time
with `--mailto`.

See `--help` for all flags (`--repo`, `--user`, `--mailto`, `--bin-dir`,
`--no-sudoers`, `--uninstall`).

### Run the pipeline manually

```bash
# Linux (as the configured service user)
sudo systemctl start ash-secupdate.service

# Or directly
./scripts/secupdates/secupdate.sh
```

### Inspect

```bash
# Linux
journalctl -u ash-secupdate.service --since today
systemctl list-timers ash-secupdate.timer

# macOS (launchd)
tail -f ~/Library/Logs/ash-secupdate.log
```

## Design

### Failure reporting is the point

The service is only useful if a failure is *immediately debuggable from the
email*. Three mechanisms work together to make that true:

- **Named failing step.** [secupdate.sh](secupdate.sh) installs a Bash `ERR`
  trap that logs `FAILED rc=… at file:line cmd: …`, so a silent `set -e` exit
  instead names the exact command that failed.
- **Byte-capped, signature-filtered capture.** The failure-mail helper does
  *not* use `journalctl -n N`. A crashing Go tool (e.g. govulncheck) can dump
  thousands of goroutine-stack lines, and a naive last-N capture keeps only the
  stack tail and drops the actual error. Instead it greps for the failure
  signature (`panic:`, `FAILED`, `Error`, `[secupdate]` step markers, …) and
  then **byte-caps** the tail (`tail -c 20000`), so the panic header and the
  failing step survive regardless of stack-dump volume.
- **Verbose scanners.** The Makefile `govulncheck` target runs with
  `-show verbose` (file:line for each finding) and a `-version` fail-fast check
  so a broken tool fails loudly instead of after a long scan.

### Noninteractive privilege

The service runs as an unprivileged user but needs root for OS updates under
systemd (no TTY). A NOPASSWD sudoers fragment grants *only* the two specific
commands. sudoers matches argv **literally** (full path + args), so:

- [secupdate.sh](secupdate.sh) resolves binaries with `command -v` and invokes
  them by full path (`env DEBIAN_FRONTEND=noninteractive /usr/bin/apt-get update`).
- The generated sudoers rules use those same full paths. `unattended-upgrade`
  takes **no** trailing argument — the installed version has no
  `--non-interactive` flag, and a rule with a nonexistent arg would never match.

The installer validates the fragment with `visudo -cf` before installing.

### Portability

[install-secupdate-service.sh](install-secupdate-service.sh) abstracts the
scheduler: systemd units on Linux, a launchd `StartCalendarInterval` agent on
macOS. On macOS, `softwareupdate --install --all` still requires sudo, so OS
updates remain interactive unless the agent runs as root (not recommended); the
repo/security-check portion runs unattended.

### Concurrency & safety

- A lock directory prevents overlapping runs (stale locks are reclaimed).
- The repo must be clean before pull; a dirty tree aborts rather than
  committing unrelated work.
- The systemd service sets a deliberately restricted `Environment=PATH=`,
  including the host's actual `go` location, so `make security`/`make lint test`
  resolve their toolchains under systemd.

## Testing changes

Before committing changes to these scripts, run the repository gates:

```bash
make lint test
```

`bash -n` each shell script, and after editing the installer, confirm the
generated unit still matches the live one on a configured host.
