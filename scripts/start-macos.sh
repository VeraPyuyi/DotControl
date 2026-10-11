#!/bin/bash
set -euo pipefail
if [[ "$(uname -s)" != Darwin ]]; then echo 'Run this on macOS.' >&2; exit 1; fi
project_root="$(cd "$(dirname "$0")/.." && pwd)"
source "$project_root/scripts/macos-signature.sh"
mode="${1:-local}"
case "$mode" in local|tunnel) ;; *) echo 'Usage: bash scripts/start-macos.sh [local|tunnel]' >&2; exit 1 ;; esac
binary="$HOME/Library/Application Support/DotControl/bin/dotcontrol"
if [[ ! -x "$binary" || ! -d /Applications/CuaDriver.app ]]; then
  echo 'Run the Mac installer first.' >&2; exit 1
fi
verify_cua_macos_signature /Applications/CuaDriver.app
open -n -g /Applications/CuaDriver.app --args serve
echo 'Keep this terminal open. Use dotcontrol pause/resume in another terminal to control actions.'
if [[ "$mode" == local ]]; then
  exec "$binary" serve --no-tunnel
else
  exec "$binary" serve
fi
