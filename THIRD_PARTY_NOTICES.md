# Third-party notices

DotControl source is MIT licensed. Bundled notices are in `third_party/licenses/`; `third_party/go-modules.json` records module versions and license-file hashes. The installers retain the vendor archives and their included notices. Do not remove those files from redistributed packages.

| Component | Version | License / source |
| --- | --- | --- |
| Go toolchain and runtime | tested 1.27.2 | BSD-style, Go-1.27.2-LICENSE |
| MCP Go SDK | 1.7.0 | Apache-2.0 transition with retained MIT contributions; original full notice included, github.com/modelcontextprotocol/go-sdk |
| go-keyring | 0.2.6 | MIT, github.com/zalando/go-keyring |
| gofrs/flock | 0.12.1 | BSD-style, github.com/gofrs/flock |
| Cua Driver | 0.34.1 | MIT, Cua-0.34.1-MIT.txt; [fixed release](https://github.com/trycua/cua/releases/tag/cua-driver-rs-v0.34.1) |
| Official tunnel-client | 0.0.16 | Apache-2.0 with NOTICE; [fixed release](https://github.com/openai/tunnel-client/releases/tag/v0.0.16) |

The full upstream Windows tunnel-client dependency notice is retained as `tunnel-client-v0.0.16-windows-amd64-licenses.txt`. Version 0.2 bundles official per-architecture macOS tunnel-client release archives instead of Homebrew; packaging copies each archive's license and notice files into the payload. Cua libraries used here are the Driver distribution, not unrelated hosted products. Cua Driver can have its own content-free telemetry settings; consult its release documentation.

All Go dependencies, including indirect libraries used by the application or its tests, are pinned in go.mod/go.sum. Their original license texts, rather than a newly inferred license classification, control redistribution. Tailscale is an optional separately installed service, not bundled. OpenAI, Cua and Tailscale names belong to their respective owners.
