# Apt Repository Publishing

This repository can publish Linux `.deb` artifacts to a static apt repository hosted on GitHub Pages.

## Security Model

- Publishing happens only in GitHub Actions.
- The signing key stays in GitHub Secrets and is imported into a temporary `GNUPGHOME` during the publish job.
- The `gh-pages` branch is the only publishing target and should be protected as a deployment branch.
- Package files are immutable: new releases add new versioned `.deb` files instead of overwriting existing ones.
- The repository metadata is signed with `InRelease` and `Release.gpg` so clients can verify integrity before installing.

## Repository Layout

```
pool/main/a/ash/*.deb
dists/jammy/main/binary-amd64/Packages
dists/jammy/main/binary-amd64/Packages.gz
dists/jammy/main/binary-amd64/Packages.xz
dists/jammy/main/binary-arm64/Packages
dists/jammy/Release
dists/jammy/InRelease
dists/jammy/Release.gpg
```

The initial implementation publishes `jammy` and `noble` metadata. Extend the suite list only after validating the target Ubuntu releases.

## Client Setup

```bash
curl -fsSL https://<owner>.github.io/ash/ash-archive-keyring.asc | sudo gpg --dearmor -o /usr/share/keyrings/ash-archive-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/ash-archive-keyring.gpg] https://<owner>.github.io/ash/ jammy main" | sudo tee /etc/apt/sources.list.d/ash.list
sudo apt-get update
sudo apt-get install ash
```

Replace `<owner>` with the GitHub account or organization that owns the Pages site.

## Operational Rules

1. Build and test release artifacts on the tag that will be published.
2. Keep the signing key offline except for the publish job secret import.
3. Rotate the signing key if the secret is exposed or the deployment environment changes.
4. Remove bad releases by publishing a new repository snapshot rather than mutating old package filenames.
5. Keep the publish branch limited to the generated apt tree and the public archive key.
