# Release workflow secrets

This guide is for engineers maintaining release automation in
`Jonconradt/ash`. Configure repository secrets at
**GitHub → Jonconradt/ash → Settings → Secrets and variables → Actions**.
The equivalent CLI commands below use `gh` and require `gh auth login`.

Check which secret names are configured with:

```bash
gh secret list --repo Jonconradt/ash
```

GitHub does not display secret values after saving them. Listing a secret only
confirms its name exists; it does not verify its value, expiry, or permissions.

## Nightly release and release notes

| Secret | Required | Value |
| --- | --- | --- |
| `RELEASE_GITHUB_TOKEN` | Yes | Fine-grained personal access token for the `Jonconradt/ash` repository. Grant **Contents: Read and write** and **Actions: Read-only**. The nightly workflow uses it to check out the repository and allow Make to push the release branch and tag. The dedicated token is needed so pushing a tag starts the tag-triggered release workflow. |
| `ASH_AI_ENDPOINT` | Yes | Base URL for an AI API reachable from a GitHub-hosted runner and supported by ash. Use the endpoint format specified by the provider. A local-only address such as `localhost` will not work on GitHub-hosted runners. |
| `ASH_AI_MODEL` | Yes | Exact model identifier accepted by the configured endpoint. |
| `ASH_AI_AUTH_TOKEN` | Conditional | Provider API credential, if the endpoint requires bearer-token authentication. Leave unset for an endpoint that does not require it. |

The nightly workflow maps `RELEASE_GITHUB_TOKEN` to `GH_TOKEN` and passes the
AI settings to `make release CHANNEL=nightly`. It fails before the release
starts if the GitHub token, endpoint, or model is missing. The endpoint and
model are also used to generate release notes.

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
ID. If authentication is required, add its API token without placing it in
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
