# Contributing

## Before submitting

First-time setup: install the required developer toolchain (Go, Node.js/npm,
Python3, `uv`, Rust/`cargo`, and the Go lint/security tools) for macOS
(Homebrew) or Debian/Ubuntu (apt):

```bash
make config
```

Run:

```bash
make check-static
make check-security
make test
```

`make config` installs the repository-managed Git hooks. The pre-push hook
validates each exact outgoing commit in a clean temporary worktree and blocks
the push if static checks, security/dependency/secret scanning, or tests fail.
This is a local safeguard and can be bypassed with `git push --no-verify`; it is
not a GitHub-enforced status check. Do not bypass it for release branches or
tags.

Run install once to configure the local shell integration:

```bash
make install
```

For the full local verification pass, run:

```bash
make verify
```

The lint/test/verify targets are quiet by default: each step prints a single
`ok` line on success and dumps its full output only when it fails. To see the
full streaming output while debugging, prefix the target with `V=1`:

```bash
make V=1 lint
make V=1 test
make V=1 verify
```

The root and Go-plugin lint commands wait for the shared `golangci-lint` lock
when another lint process is running, rather than failing on lock contention.
They serialize access instead of bypassing the lock.

Use `V=1` with any Make target to stream its output locally. Tag-triggered CI
focuses on artifact generation and platform validation; it does not rerun the
local source and dependency checks.

## Testing expectations

- Add regression tests for new runtime behavior.
- Prefer table-driven tests for config and parsing logic.
- Keep tests deterministic and avoid depending on external services.

## Release expectations

- Run `make update-dependencies` to check the 12 pinned Makefile tools against
  their upstream stable releases and check Go against both upstream releases
  and the local package manager. This is also the first prerequisite of
  `make release`. Checks require network access and interactive input; nothing
  is automatically approved in CI or when input ends.
- Approve tool updates individually. Newer pins, including Go pseudo-versions,
  are never downgraded to older stable releases. The updater preserves other
  Makefile content and does not change environment or command-line overrides.
- On macOS, the updater refreshes Homebrew metadata and, with approval, attempts
  `brew upgrade go`. If that fails or the installed compiler remains behind,
  it prompts you to upgrade manually and rechecks. On Linux, it queries apt,
  dnf, or pacman and prompts for a manual Go upgrade; refresh apt/pacman package
  indexes beforehand. Other Linux package managers are reported as unsupported.
  If distro packages lag upstream, the updater reports both versions rather
  than claiming the package candidate is the latest upstream release.
- The Go directive is only advanced, with approval, to the verified locally
  installed stable compiler version. No toolchain auto-download is used by the
  updater. With separate approval, it runs `go get -u ./...` followed by
  `go mod tidy`. These commands can also change the module's Go requirements;
  command failures stop the updater and retain changes for review.
- Review updates, run `make config` to refresh installed development tools,
  run `make lint test`, and commit the changes before retrying `make release`.
  The existing clean-worktree release check remains in force. The updater does
  not commit, stash, or discard changes; failures retain changes already applied.
- All release packaging jobs run on Linux. macOS tarballs for both architectures
  are cross-compiled without Rust plugins; CI validates their Mach-O architecture
  and archive contents, not macOS runtime behavior. Linux and FreeBSD continue
  to include Rust plugins.
- Use `make release-artifacts RELEASE_TARGET_OSES=darwin RELEASE_TARGET_ARCHES="amd64 arm64" RELEASE_MACOS_PKG=0 RELEASE_VERSION=v1.2.3`
  to build macOS tarballs from Linux. `make release` still requires macOS for
  local `.pkg` creation and release-note generation; it does not upload local
  build outputs to CI. Scheduled nightly orchestration runs on Ubuntu, verifies
  before tagging, and delegates artifact generation to the tag-triggered workflow.
- Keep the working tree clean before publishing.
- Ensure release artifacts are built and validated locally before tagging.
- Run `make verify` locally before `make release`. The pre-push hook runs the
  focused static, security, and test gates against outgoing commits. Tag-triggered
  packaging jobs retain artifact and platform validation but no longer repeat
  source checks. Nightly automation runs the pre-push gate before creating tags.
- The external Go install check fetches the release tag directly from GitHub
  with `GONOPROXY` and `GONOSUMDB` scoped to `github.com/Jonconradt/ash`.
  This avoids proxy and checksum-database availability delays for newly pushed
  tags; dependencies still use the public proxy and checksum verification.
- `make release` creates a beta tag; use `make release CHANNEL=stable` for a
  verified stable release branch, or `make release CHANNEL=nightly` for a
  nightly prerelease.
- Fetch tags before publishing. Never delete or move a release tag, even after
  a failed build: Go's checksum database may already have indexed it. Nightly
  retries on the same UTC day append `.1`, `.2`, and so on; beta retries use
  the next beta counter.
- Non-nightly release preparation runs the existing `make site-locales` Google
  Translate generator in an isolated temporary copy and attaches the generated
  website as a release artifact. Nightly releases skip website translation.
  Keep translation failures visible for releases that run the generator; do
  not substitute incomplete or stale generated output.
- The scheduled nightly workflow requires `RELEASE_GITHUB_TOKEN` with contents
  write and actions read permissions, plus `ASH_AI_ENDPOINT` and `ASH_AI_MODEL`
  secrets for release notes. An optional provider credential may be set with
  `ASH_AI_AUTH_TOKEN`.
- See [docs/release-secrets.md](docs/release-secrets.md) for setup instructions,
  required permissions, commands, and rotation guidance for release secrets.
- Release notes are generated by piping bounded Git history from Make into
  `ash`; the resulting Markdown is stored in the annotated release tag and
  published as the GitHub Release body. Configure `ASH_AI_ENDPOINT` and `ASH_AI_MODEL`
  before running a release.
