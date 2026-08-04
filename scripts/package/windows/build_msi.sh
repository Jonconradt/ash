#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  build_msi.sh --app-name <name> --version <vX.Y.Z> --arch <amd64|arm64> --binary <path> --output <path.msi>
EOF
}

app_name=""
version=""
arch=""
binary_path=""
output_path=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --app-name)
      app_name="${2:-}"
      shift 2
      ;;
    --version)
      version="${2:-}"
      shift 2
      ;;
    --arch)
      arch="${2:-}"
      shift 2
      ;;
    --binary)
      binary_path="${2:-}"
      shift 2
      ;;
    --output)
      output_path="${2:-}"
      shift 2
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

if [[ -z "$app_name" || -z "$version" || -z "$arch" || -z "$binary_path" || -z "$output_path" ]]; then
  echo "all arguments are required" >&2
  usage >&2
  exit 1
fi

if [[ ! -f "$binary_path" ]]; then
  echo "binary not found: $binary_path" >&2
  exit 1
fi

if ! command -v wixl >/dev/null 2>&1; then
  echo "wixl command not found. Install msitools: sudo apt-get install msitools" >&2
  exit 1
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 command not found" >&2
  exit 1
fi

case "$arch" in
  amd64) platform="x64" ;;
  arm64) platform="arm64" ;;
  *)
    echo "unsupported architecture: $arch (expected amd64 or arm64)" >&2
    exit 1
    ;;
esac

# MSI version must be numeric X.Y.Z; strip v prefix and any prerelease suffix
msi_version="$(echo "${version#v}" | grep -Eo '^[0-9]+\.[0-9]+\.[0-9]+')"
if [[ -z "$msi_version" ]]; then
  echo "cannot extract numeric version from: $version" >&2
  exit 1
fi

# Stable per-app UpgradeCode ensures Windows Installer detects prior installations
upgrade_code="$(python3 -c "import uuid; print(str(uuid.uuid5(uuid.NAMESPACE_DNS, '${app_name}')))")"

output_dir="$(dirname "$output_path")"
mkdir -p "$output_dir"

stage_dir="$(mktemp -d)"
cleanup() {
  rm -rf "$stage_dir"
}
trap cleanup EXIT

cp "$binary_path" "$stage_dir/${app_name}.exe"

wxs_path="$stage_dir/${app_name}.wxs"
cat > "$wxs_path" <<WXSEOF
<?xml version="1.0" encoding="UTF-8"?>
<Wix xmlns="http://schemas.microsoft.com/wix/2006/wi">
  <Product Id="*"
           Name="${app_name}"
           Language="1033"
           Version="${msi_version}"
           Manufacturer="${app_name}"
           UpgradeCode="${upgrade_code}">
    <Package InstallerVersion="200"
             Compressed="yes"
             InstallScope="perMachine"
             Platform="${platform}"/>
    <MajorUpgrade DowngradeErrorMessage="A newer version of ${app_name} is already installed."/>
    <MediaTemplate EmbedCab="yes"/>
    <Directory Id="TARGETDIR" Name="SourceDir">
      <Directory Id="ProgramFiles64Folder">
        <Directory Id="INSTALLFOLDER" Name="${app_name}">
          <Component Id="ApplicationFiles" Guid="*">
            <File Id="MainExecutable"
                  Name="${app_name}.exe"
                  Source="${stage_dir}/${app_name}.exe"
                  KeyPath="yes"/>
          </Component>
        </Directory>
      </Directory>
    </Directory>
    <Feature Id="ProductFeature" Title="${app_name}" Level="1">
      <ComponentRef Id="ApplicationFiles"/>
    </Feature>
  </Product>
</Wix>
WXSEOF

wixl -a "$platform" -o "$output_path" "$wxs_path"

echo "created package: $output_path"
