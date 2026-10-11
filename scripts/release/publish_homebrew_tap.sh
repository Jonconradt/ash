#!/usr/bin/env bash
set -euo pipefail

release_tag="${1:?usage: publish_homebrew_tap.sh <release-tag> <checksums-file>}"
checksums_file="${2:?usage: publish_homebrew_tap.sh <release-tag> <checksums-file>}"
owner="Jonconradt"
tap_repo="homebrew-ash"
api_url="https://api.github.com"

if [[ "$release_tag" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
	formula_name="ash"
	class_name="Ash"
elif [[ "$release_tag" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)-beta\.[1-9][0-9]*$ ]]; then
	formula_name="ash-beta"
	class_name="AshBeta"
elif [[ "$release_tag" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)-nightly\.[0-9]{8}$ ]]; then
	formula_name="ash-nightly"
	class_name="AshNightly"
else
	echo "Unsupported Homebrew release tag: $release_tag" >&2
	exit 1
fi

token="${HOMEBREW_TAP_GITHUB_TOKEN:?Set the HOMEBREW_TAP_GITHUB_TOKEN repository secret}"
if [[ ! -s "$checksums_file" ]]; then
	echo "Release checksum manifest is missing or empty: $checksums_file" >&2
	exit 1
fi
version="${release_tag#v}"

sha256_for() {
	local artifact="$1"
	local hashes
	hashes="$(awk -v artifact="$artifact" '$2 == artifact { print $1 }' "$checksums_file")"
	if [[ "$(printf '%s\n' "$hashes" | grep -c .)" -ne 1 || ! "$hashes" =~ ^[[:xdigit:]]{64}$ ]]; then
		echo "Expected exactly one SHA-256 checksum for $artifact in $checksums_file" >&2
		return 1
	fi
	printf '%s' "$hashes"
}

darwin_amd64_sha="$(sha256_for "ash-${release_tag}-darwin-amd64.tar.gz")"
darwin_arm64_sha="$(sha256_for "ash-${release_tag}-darwin-arm64.tar.gz")"
linux_amd64_sha="$(sha256_for "ash-${release_tag}-linux-amd64.tar.gz")"
linux_arm64_sha="$(sha256_for "ash-${release_tag}-linux-arm64.tar.gz")"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
api_response="$tmp_dir/api-response.json"
auth_header="Authorization: Bearer ${token}"
api_headers=(-H "$auth_header" -H "Accept: application/vnd.github+json" -H "X-GitHub-Api-Version: 2022-11-28")

status="$(curl --silent --show-error --output "$api_response" --write-out '%{http_code}' "${api_headers[@]}" "$api_url/repos/$owner/$tap_repo")"
if [[ "$status" == "404" ]]; then
	status="$(curl --silent --show-error --output "$api_response" --write-out '%{http_code}' \
		-X POST "${api_headers[@]}" \
		-H "Content-Type: application/json" \
		--data '{"name":"homebrew-ash","description":"Homebrew tap for ash","private":false,"auto_init":true}' \
		"$api_url/user/repos")"
	if [[ "$status" != "201" ]]; then
		echo "Could not create public tap repository ($status): $(cat "$api_response")" >&2
		exit 1
	fi
	echo "Created public repository $owner/$tap_repo"
elif [[ "$status" != "200" ]]; then
	echo "Could not inspect tap repository ($status): $(cat "$api_response")" >&2
	exit 1
fi

auth_config="$(printf 'x-access-token:%s' "$token" | base64 | tr -d '\n')"
git -c "http.extraheader=AUTHORIZATION: basic $auth_config" \
	clone --quiet "https://github.com/$owner/$tap_repo.git" "$tmp_dir/tap"
mkdir -p "$tmp_dir/tap/Formula"
cat > "$tmp_dir/tap/Formula/${formula_name}.rb" <<FORMULA
class ${class_name} < Formula
  desc "AI assistant for bash, Fish, and zsh"
  homepage "https://github.com/Jonconradt/ash"
  version "$version"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/Jonconradt/ash/releases/download/${release_tag}/ash-${release_tag}-darwin-arm64.tar.gz"
      sha256 "$darwin_arm64_sha"
    end
    on_intel do
      url "https://github.com/Jonconradt/ash/releases/download/${release_tag}/ash-${release_tag}-darwin-amd64.tar.gz"
      sha256 "$darwin_amd64_sha"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/Jonconradt/ash/releases/download/${release_tag}/ash-${release_tag}-linux-arm64.tar.gz"
      sha256 "$linux_arm64_sha"
    end
    on_intel do
      url "https://github.com/Jonconradt/ash/releases/download/${release_tag}/ash-${release_tag}-linux-amd64.tar.gz"
      sha256 "$linux_amd64_sha"
    end
  end

  def install
    os = OS.mac? ? "darwin" : "linux"
    arch = Hardware::CPU.arm? ? "arm64" : "amd64"
    bin.install "ash-v#{version}-#{os}-#{arch}" => "ash"
    bin.install "ash-broker"
    (lib / "ash").install "plugins"
    man1.install "usr/share/man/man1/ash.1"
    Dir["usr/share/man/*/man1/ash.1"].each do |page|
      locale = File.basename(File.dirname(File.dirname(page)))
      (share / "man" / locale / "man1").install page
    end
    (share / "licenses" / "ash").install "LICENSE"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/ash version")
  end
end
FORMULA

git -C "$tmp_dir/tap" config user.name "github-actions[bot]"
git -C "$tmp_dir/tap" config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git -C "$tmp_dir/tap" add "Formula/${formula_name}.rb"
if git -C "$tmp_dir/tap" diff --cached --quiet; then
	echo "Homebrew formula is already current for $release_tag"
	exit 0
fi
git -C "$tmp_dir/tap" commit -m "Update Homebrew formula to $release_tag" \
	-m "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
branch="$(git -C "$tmp_dir/tap" branch --show-current)"
git -C "$tmp_dir/tap" -c "http.extraheader=AUTHORIZATION: basic $auth_config" push origin "$branch"
echo "Updated $owner/$tap_repo/Formula/${formula_name}.rb to $release_tag"
