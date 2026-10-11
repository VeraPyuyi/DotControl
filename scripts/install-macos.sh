#!/bin/bash
set -euo pipefail
project_root="$(cd "$(dirname "$0")/.." && pwd)"
source "$project_root/scripts/macos-signature.sh"
device_name="${1:-DotControl Mac}"
install_root="$HOME/Library/Application Support/DotControl"
apps_root="$HOME/Applications"
installed_driver_app="/Applications/CuaDriver.app"
go_bin="${DOTCONTROL_GO:-go}"
if [[ "$(uname -s)" != Darwin ]]; then echo 'Run this installer on macOS.' >&2; exit 1; fi
if [[ "$(sw_vers -productVersion | cut -d. -f1)" -lt 14 ]]; then echo 'macOS 14+ required.' >&2; exit 1; fi
if ! xcrun --find swiftc >/dev/null 2>&1; then
  echo 'Install Apple Command Line Tools with: xcode-select --install, then rerun.' >&2; exit 1
fi
mkdir -p "$install_root/bin" "$install_root/downloads" "$apps_root"
case "$(uname -m)" in
  arm64) asset_arch=arm64; go_arch=arm64; asset_sha=83ffed8d41ef0f133976d594b02eb86dd59ef4a8721396b6e25078a59c6056d6 ;;
  x86_64) asset_arch=x86_64; go_arch=amd64; asset_sha=c485781a64b6f75ea46f63d03c5b4f646e0f1dcea2c7547805f235259193a156 ;;
  *) echo 'Unsupported architecture.' >&2; exit 1 ;;
esac
prebuilt="$project_root/prebuilt/dotcontrol-darwin-$go_arch"
use_prebuilt=false
if [[ -d "$project_root/prebuilt" && "${DOTCONTROL_FROM_SOURCE:-0}" != 1 ]]; then
  if [[ ! -f "$prebuilt" || ! -f "$project_root/prebuilt/SHA256SUMS" ]]; then
    echo 'Incomplete prebuilt package; extract the complete ZIP again.' >&2; exit 1
  fi
  (cd "$project_root/prebuilt" && shasum -a 256 -c SHA256SUMS)
  use_prebuilt=true
elif ! command -v "$go_bin" >/dev/null; then
  echo 'Source installation requires Go 1.25+; the Mac installation ZIP includes prebuilt binaries.' >&2; exit 1
fi
tunnel_bin=tunnel-client
if [[ "${DOTCONTROL_SKIP_TUNNEL:-0}" != 1 ]]; then
  if ! command -v tunnel-client >/dev/null; then
    if ! command -v brew >/dev/null; then
      echo 'Install official Homebrew from https://brew.sh and rerun, or use DOTCONTROL_SKIP_TUNNEL=1 for local desktop diagnostics.' >&2; exit 1
    fi
    brew install openai/tools/tunnel-client
  fi
  tunnel_version="$(tunnel-client --version)"
  if [[ ! "$tunnel_version" =~ (^|[[:space:]])0\.0\.16(\+|[[:space:]]|$) ]]; then
    echo 'Expected official tunnel-client 0.0.16. Version changed; review before updating the pin.' >&2; exit 1
  fi
  tunnel_bin="$(command -v tunnel-client)"
fi
archive="$install_root/downloads/cua-driver-rs-0.34.1-darwin-$asset_arch.tar.gz"
if [[ ! -f "$archive" ]]; then
  bundled_archive="$project_root/bundled/$(basename "$archive")"
  if [[ -f "$bundled_archive" ]]; then
    printf '%s  %s\n' "$asset_sha" "$bundled_archive" | shasum -a 256 -c -
    cp "$bundled_archive" "$archive"
  else
    curl --fail --location --retry 2 --proto '=https' --tlsv1.2 "https://github.com/trycua/cua/releases/download/cua-driver-rs-v0.34.1/$(basename "$archive")" -o "$archive.partial"
    printf '%s  %s\n' "$asset_sha" "$archive.partial" | shasum -a 256 -c -
    mv "$archive.partial" "$archive"
  fi
fi
printf '%s  %s\n' "$asset_sha" "$archive" | shasum -a 256 -c -
driver_root="$install_root/cua-0.34.1-$asset_arch"
mkdir -p "$driver_root"
tar -xzf "$archive" -C "$driver_root"
driver_app="$(find "$driver_root" -type d -name CuaDriver.app -prune -print)"
driver_cli="$(find "$driver_root" -type d -name '*.app' -prune -o -type f -name cua-driver -print)"
if [[ -z "$driver_app" || "$driver_app" == *$'\n'* || -z "$driver_cli" || "$driver_cli" == *$'\n'* ]]; then
  echo 'Expected exactly one CuaDriver.app and one cua-driver CLI; review upstream packaging.' >&2; exit 1
fi
# A matching archive hash or --version is not a macOS signature check.
# Validate both the daemon bundle and the separate CLI before executing either.
verify_cua_macos_signature "$driver_app"
verify_cua_macos_signature "$driver_cli"
# Cua 0.34.1's permission host opens this absolute path, not ~/Applications.
# Preserve existing bundles and let Finder handle any required OS approval.
if [[ ! -e "$installed_driver_app" && -e "$apps_root/CuaDriver.app" ]]; then
  echo 'Earlier DotControl installed CuaDriver in ~/Applications. Stop DotControl and CuaDriver, then move CuaDriver.app to /Applications with Finder and rerun.' >&2
  exit 1
fi
if [[ -e "$installed_driver_app" ]]; then
  verify_cua_macos_signature "$installed_driver_app"
  existing_version="$("$installed_driver_app/Contents/MacOS/cua-driver" --version)"
  if [[ ! "$existing_version" =~ (^|[[:space:]])0\.34\.1(\+|[[:space:]]|$) ]]; then
    echo 'Existing CuaDriver.app is not 0.34.1; left unchanged. Resolve its version before installing.' >&2; exit 1
  fi
  echo 'Existing CuaDriver.app 0.34.1 preserved.'
else
  if [[ ! -w /Applications ]]; then
    echo "Use Finder to copy $driver_app to /Applications, then rerun. The installer does not elevate privileges." >&2
    exit 1
  fi
  ditto "$driver_app" "$installed_driver_app"
  verify_cua_macos_signature "$installed_driver_app"
fi
helper_app="$apps_root/DotControlActivity.app"
mkdir -p "$helper_app/Contents/MacOS"
cp "$project_root/native/macos/Info.plist" "$helper_app/Contents/Info.plist"
xcrun swiftc -O -framework AppKit -framework CoreGraphics "$project_root/native/macos/Activity.swift" -o "$helper_app/Contents/MacOS/DotControlActivity"
codesign --force --sign - --identifier org.dotcontrol.activity "$helper_app"
cd "$project_root"
if [[ "$use_prebuilt" == true ]]; then
  install -m 755 "$prebuilt" "$install_root/bin/dotcontrol"
else
  "$go_bin" build -trimpath -o "$install_root/bin/dotcontrol" ./cmd/dotcontrol
fi
mkdir -p "$install_root/licenses"
cp LICENSE "$install_root/licenses/"
ditto third_party "$install_root/licenses/third_party"
if [[ ! -f "$install_root/config.json" ]]; then
  "$install_root/bin/dotcontrol" setup --name "$device_name" --driver "$driver_cli" --tunnel "$tunnel_bin" --activity-helper "$helper_app/Contents/MacOS/DotControlActivity"
else
  echo "Existing device identity preserved. Review paths in $install_root/config.json."
fi
echo 'Installed. Follow docs/INSTALL.md for system permissions and CuaDriver.app startup.'
echo 'No permissions, login items or security exceptions were configured automatically.'
