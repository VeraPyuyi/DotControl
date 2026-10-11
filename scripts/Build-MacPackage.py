"""Build a credential-free Mac handoff ZIP from an explicit source archive.

Run Build-Release.ps1 and create dotcontrol-source.zip first. Requires only the
Python standard library; verifies binaries and upstream archives before bundling.
"""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import zipfile


def sha256(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release-dir", type=Path, default=Path("dist/release"))
    parser.add_argument("--cua-cache", type=Path, required=True)
    args = parser.parse_args()
    release = args.release_dir.resolve()
    source = release / "dotcontrol-source.zip"
    hashes = {r["file"]: r["sha256"] for r in json.loads((release / "SHA256.json").read_text(encoding="utf-8-sig"))}
    prefix = "DotControl-Mac/"
    entries = {}
    with zipfile.ZipFile(source) as archive:
        for item in archive.infolist():
            if item.is_dir():
                continue
            path = PurePosixPath(item.filename)
            if (path.is_absolute() or ".." in path.parts or "\\" in item.filename
                    or any(p in {".state", ".tools", ".git", "dist"} for p in path.parts)):
                raise ValueError(f"Unexpected source archive path: {item.filename}")
            if item.filename in entries:
                raise ValueError(f"Duplicate source path: {item.filename}")
            entries[item.filename] = archive.read(item)
    lock = json.loads(entries["dependencies.lock.json"])
    entries["MAC_START_HERE.md"] = (entries["docs/MAC_START_HERE.md"]
        .replace(b"](INSTALL.md)", b"](docs/INSTALL.md)")
        .replace(b"](MAC_SIGNATURE_RECOVERY.md)", b"](docs/MAC_SIGNATURE_RECOVERY.md)"))
    binary_sums = []
    for arch in ("arm64", "amd64"):
        name = f"dotcontrol-darwin-{arch}"
        path = release / name
        digest = sha256(path)
        if digest != hashes[name]:
            raise ValueError(f"Release binary checksum mismatch: {name}")
        entries[f"prebuilt/{name}"] = path.read_bytes()
        binary_sums.append(f"{digest}  {name}\n")
    entries["prebuilt/SHA256SUMS"] = "".join(binary_sums).encode()
    for arch in ("macos-arm64", "macos-x86_64"):
        asset = lock["cua_driver"]["assets"][arch]
        path = args.cua_cache / asset["file"]
        if sha256(path) != asset["sha256"]:
            raise ValueError(f"Upstream archive checksum mismatch: {path.name}")
        entries[f"bundled/{path.name}"] = path.read_bytes()
    entries["PACKAGE_SHA256SUMS"] = "".join(
        f"{hashlib.sha256(data).hexdigest()}  {name}\n" for name, data in sorted(entries.items())
    ).encode()
    output = release / "DotControl-Mac.zip"
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=6) as archive:
        for name, data in sorted(entries.items()):
            info = zipfile.ZipInfo(prefix + name, (2026, 10, 11, 0, 0, 0))
            executable = name.endswith(".sh") or name.startswith("prebuilt/dotcontrol-")
            info.create_system = 3
            info.external_attr = (0o100755 if executable else 0o100644) << 16
            info.compress_type = zipfile.ZIP_STORED if name.endswith(".tar.gz") else zipfile.ZIP_DEFLATED
            archive.writestr(info, data)
    with zipfile.ZipFile(output) as archive:
        if set(archive.namelist()) != {prefix + name for name in entries}:
            raise ValueError("Packaged entry set differs from expected inputs")
        for name, expected in entries.items():
            if archive.read(prefix + name) != expected:
                raise ValueError(f"Packaged byte mismatch: {name}")
    report = dict(file=output.name, bytes=output.stat().st_size, sha256=sha256(output),
                  verified_entries=len(entries), source_archive_sha256=sha256(source),
                  architectures=["arm64", "amd64"], mac_native_execution="not_verified")
    (release / "MAC_PACKAGE_VALIDATION.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
