# Release workflow secrets

This guide is for engineers maintaining release automation in
`Jonconradt/ash`. Configure repository secrets at
**GitHub → Jonconradt/ash → Settings → Secrets and variables → Actions**.
The equivalent CLI commands below use Bash and `gh` and require `gh auth login`.
On macOS, run `bash` first if your interactive shell is zsh.

Check which secret names are configured with:

```bash
gh secret list --repo Jonconradt/ash
```

GitHub does not display secret values after saving them. Listing a secret only
confirms its name exists; it does not verify its value, expiry, or permissions.

## Nightly release and release notes

| Secret | Required | Value |
| --- | --- | --- |
| `RELEASE_GITHUB_TOKEN` | Yes | Fine-grained personal access token for the `Jonconradt/ash` repository. Grant **Contents: Read and write** and **Actions: Read-only**. It authenticates nightly tag pushes, GitHub Release creation, and package repository pushes. Unlike `GITHUB_TOKEN`, these events trigger the next workflows in the release chain. |
| `ASH_AI_ENDPOINT` | Yes | Base URL for an AI API reachable from a GitHub-hosted runner and supported by ash. Use the endpoint format specified by the provider. A local-only address such as `localhost` will not work on GitHub-hosted runners. |
| `ASH_AI_MODEL` | Yes | Exact model identifier accepted by the configured endpoint. |
| `ASH_AI_AUTH_TOKEN` | Conditional | Provider API credential. ash requires it for public/cloud endpoints other than AWS Bedrock, even if the service itself accepts unauthenticated requests. Local/LAN endpoints may omit it; Bedrock uses the AWS credential chain instead. |

The nightly workflow maps `RELEASE_GITHUB_TOKEN` to `GH_TOKEN` and passes the
AI settings to `make release CHANNEL=nightly`. It fails before the release
starts if the GitHub token, endpoint, or model is missing. The endpoint and
model are also used to generate release notes.
For a public Ollama or OpenAI-compatible endpoint, configure all three AI
secrets, including `ASH_AI_AUTH_TOKEN`.

### Create `RELEASE_GITHUB_TOKEN`

1. On GitHub, open **Settings → Developer settings → Personal access tokens →
   Fine-grained tokens → Generate new token**.
2. Name the token, select an appropriate expiration, and choose **Jonconradt**
   as the resource owner.
3. Select **Only select repositories**, then choose **ash**.
4. Under repository permissions, set **Contents** to **Read and write** and
   **Actions** to **Read-only**. Keep other permissions at **No access** unless
   organization policy requires otherwise.
5. Generate the token and copy it. GitHub shows the token value only at
   creation time.

Save it without putting the token in shell history:

```bash
read -rsp 'Paste the RELEASE_GITHUB_TOKEN value: ' secret
printf '\n'
printf '%s' "$secret" | gh secret set RELEASE_GITHUB_TOKEN --repo Jonconradt/ash
unset secret
```

### Configure the AI endpoint and model

Obtain the endpoint URL, model identifier, and (when required) API token from
the AI provider's documentation or account dashboard. For the endpoint and
model, the GitHub UI can be used to create a repository secret with the
matching name and value. Or set them through `gh`:

```bash
printf '%s' 'https://your-provider.example/v1' |
  gh secret set ASH_AI_ENDPOINT --repo Jonconradt/ash
printf '%s' 'your-exact-model-id' |
  gh secret set ASH_AI_MODEL --repo Jonconradt/ash
```

Replace the example values with the provider's actual endpoint and exact model
ID. For public/cloud endpoints, add its API token without placing it in
shell history:

```bash
read -rsp 'Paste the provider API token: ' secret
printf '\n'
printf '%s' "$secret" | gh secret set ASH_AI_AUTH_TOKEN --repo Jonconradt/ash
unset secret
```

## Homebrew tap publishing

`HOMEBREW_TAP_GITHUB_TOKEN` is used by the tag-triggered release workflow to
push formula updates to `Jonconradt/homebrew-ash`. Create the public tap
repository first:

```bash
gh repo create Jonconradt/homebrew-ash \
  --public \
  --description "Homebrew tap for ash"
```

Create a separate fine-grained personal access token using the steps above.
Select **homebrew-ash** as the only repository and grant **Contents: Read and
write**. Save it as `HOMEBREW_TAP_GITHUB_TOKEN` in the `Jonconradt/ash`
repository:

```bash
read -rsp 'Paste the HOMEBREW_TAP_GITHUB_TOKEN value: ' secret
printf '\n'
printf '%s' "$secret" | gh secret set HOMEBREW_TAP_GITHUB_TOKEN --repo Jonconradt/ash
unset secret
```

Keep this token scoped to the tap repository; do not reuse the nightly token.
The repository may initially be empty; the publishing script creates the first
formula commit on its default branch.

## Linux package repository signing

The APT/RPM/Pacman publishing workflow signs repository metadata and packages
using GPG:

- `GPG_PRIVATE_KEY` is the base64-encoded, armored export of the secret signing
  key. Protect the key and retain a secure offline backup.
- `GPG_PASSPHRASE` is the passphrase protecting that private key. It is needed
  when the key has a passphrase; do not set it if the key is unprotected.

Use an organization-approved signing key if one exists. Otherwise create a
dedicated key with GPG, using the interactive prompts to set the identity,
key type, expiry, and a strong passphrase:

```bash
gpg --full-generate-key
gpg --list-secret-keys --keyid-format LONG
```

Find the key ID in the listing, assign it below, then export and encode the
secret key directly into the Actions secret. The pipeline avoids putting the
private key in a file or in shell history:

```bash
set -o pipefail
KEY_ID='replace-with-the-key-id'
gpg --armor --export-secret-keys "$KEY_ID" |
  base64 | tr -d '\n' |
  gh secret set GPG_PRIVATE_KEY --repo Jonconradt/ash
```

If the key has a passphrase, save that exact passphrase as `GPG_PASSPHRASE`:

```bash
read -rsp 'Enter the GPG key passphrase: ' secret
printf '\n'
printf '%s' "$secret" | gh secret set GPG_PASSPHRASE --repo Jonconradt/ash
unset secret
```

The workflow publishes the matching public key in the package repositories;
users rely on that public key to verify repository signatures. Do not replace
or revoke the signing key without planning how existing package-manager
installations will trust the replacement.

## GitHub UI alternative

To set any value in the browser, open **Jonconradt/ash → Settings → Secrets
and variables → Actions → New repository secret**, enter the exact secret name
from the tables above, paste its value into **Secret**, then select **Add
secret**. The `gh secret set` commands update an existing secret of the same
name.

After setup, verify the names with `gh secret list --repo Jonconradt/ash`.
Values are intentionally not displayed. When a token expires or is rotated,
create a replacement with the same repository scope and permissions, update
the corresponding secret, verify the next workflow run, then revoke the old
token.

## Release readiness and workflow chain

Before a local release, fetch the current stable and prerelease tags and ensure
your macOS checkout is clean, attached to a branch, and has a Git identity:

```bash
git fetch origin --tags
git status --short
git var GIT_AUTHOR_IDENT
git var GIT_COMMITTER_IDENT
```

Repository secrets are not downloaded into your local shell. Set
`ASH_AI_ENDPOINT`, `ASH_AI_MODEL`, and any required provider credentials locally
before running Make. Authenticate Git pushes separately, for example using
`gh auth setup-git` with an appropriately authorized account.

```bash
make release CHANNEL=nightly
make release                 # beta
make release CHANNEL=stable  # full make verify, then release/vX.Y branch
```

These commands publish real release tags; do not use them as dry runs. Each
invocation freezes the version across all Make steps. Release translations
start from the existing Google Translate cache and store updates in ignored
`.cache/ash/site-translation-cache.json`, leaving tracked files unchanged.

The scheduled nightly checks the default branch at 04:00 UTC and skips when
only generated APT/RPM/Pacman repository files have changed since the latest
nightly. This prevents publishing yesterday's packages from causing another
otherwise identical nightly.
The comparison uses the latest published Nightly release, not failed tags, so
failed builds do not suppress scheduled retries. Same-day retries automatically
append an increasing counter (for example `v1.2.0-nightly.20261011.1`).
Fetch all remote tags before releasing from a local checkout. Never move or
delete release tags, including failed attempts: Go's public checksum database
can index a tag before a GitHub Release exists. Publish a new version instead;
do not disable checksum verification.
Release quality checks run the pinned Gitleaks version through Go, so a separate
`gitleaks` executable is not required. The release quality job checks out full
Git history for the history scan. Branch and tag push failures stop publication;
sync the branch and rerun release preparation rather than tagging stale code.
The secret-scan configuration retains the default rules and excludes only
SHA-256 object-key matches in the translation cache from the generic API-key
rule; credential values in that file remain subject to scanning.

The pushed annotated tag starts `release.yml`, which verifies and builds all
platform artifacts, runs smoke tests, signs checksums, and creates the GitHub
Release using `RELEASE_GITHUB_TOKEN`. The published-release event starts
`apt.yml`, which signs and commits channel-specific package repositories using
the same token. That push to the default branch starts `site.yml`; Pages
deploys from the default branch, not from a prerelease tag. Homebrew updates
run within `release.yml` using the separate tap token.

Verify workflow outcomes, not just secret names:

```bash
gh run list --repo Jonconradt/ash --workflow nightly.yml
gh run list --repo Jonconradt/ash --workflow release.yml
gh run list --repo Jonconradt/ash --workflow apt.yml
gh run list --repo Jonconradt/ash --workflow site.yml
gh run view RUN_ID --repo Jonconradt/ash --log-failed
```

Make's release dashboard waits for the tag-triggered release workflow, including
Homebrew publication, matching both the local tag name and its commit. Fetch
the tag before using the dashboard from another checkout. Linux repository publication and Pages deployment are
separate runs and must also be checked. Token values and scopes, provider/model
access, and the GPG passphrase cannot be verified through `gh secret list`.
