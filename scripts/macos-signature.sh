#!/bin/bash
# Sourced by the Mac installer and launcher. No signing or security changes.
# Team ID is pinned by Cua 0.34.1's upstream release-signature verifier:
# .github/scripts/verify_cua_driver_release_signatures.py at cua-driver-rs-v0.34.1.
verify_cua_macos_signature() {
  local target="$1"
  if ! codesign --verify --deep --strict --verbose=2 \
    -R 'anchor apple generic and certificate leaf[subject.OU] = "YCK386LBJ7"' "$target"; then
    printf 'cua_signature_verification_failed: %s\n' "$target" >&2
    printf '%s\n' 'Stopped. Compare this copy with the checksum-verified release archive before replacing it. Do not re-sign it or disable signature verification.' >&2
    return 1
  fi
}
