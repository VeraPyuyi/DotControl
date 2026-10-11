# 0.2.0-alpha.1 support and acceptance

Status is evidence-bound. This page must be updated after the final release bytes
are downloaded and tested; earlier source and 0.1 acceptance are not substituted.

| Check | Windows 11 x64 | Mac Apple Silicon | Mac Intel |
|---|---|---|---|
| Go source build | passed locally | cross-build passed | cross-build passed |
| Automated engine/UI tests | passed locally; review ongoing | CI pending | CI pending |
| Self-contained installer | first EXE packaged, final bytes pending | native build/signature gate pending | native build/signature gate pending |
| Clean system install | not verified | not verified | not verified |
| 0.2 actual cross-device and image delivery | not verified | not verified | not verified |
| Official Codex native discovery | passed on current official binary | not verified | not verified |
| Official Codex task/approval/interruption/images | not verified; disabled | not verified; disabled | not verified; disabled |

## Known limits

- Login, local runtime-key entry, OS permissions and official acknowledgements
  remain user actions. Webpage assistance uses supported host browser tools.
- Alpha installers are not publisher-signed/notarized. Cua's upstream signature
  must independently pass. The previously disputed 0.34.1 Mac archive must pass
  the native packaging gate; do not suppress that gate to produce a DMG.
- Direct Mac tunnel binaries now replace Homebrew; hashes are pinned separately
  for Apple Silicon and Intel in dependencies.lock.json.
- MCP image return, local viewing, cloud attachment creation and actual source
  display are separate states. No legacy Python uploader is bundled.
- There is no public gateway product, marketplace publication or forced update.
- A legacy running agent without graceful-shutdown support must be stopped before
  migration; installers never forcibly kill an uncertain in-flight desktop action.

See IMPLEMENTATION_0_2.md for historical failures and the regression mapping.
