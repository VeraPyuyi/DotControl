#!/usr/bin/env python3
"""Build-time packaging only; Python/Go/Swift are never required by users."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import plistlib
import shutil
import subprocess
import tarfile
import tempfile
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[1]
VERSION = '0.2.0-alpha.4'
LOCK = json.loads((ROOT / 'dependencies.lock.json').read_text())

def run(*args, **kw):
    subprocess.run([str(v) for v in args], check=True, cwd=ROOT, **kw)

def digest(path):
    with open(path, 'rb') as f:
        return hashlib.file_digest(f, 'sha256').hexdigest()

def download(asset, repo, tag, cache):
    path = cache / asset['file']
    if not path.exists() or digest(path) != asset['sha256']:
        tmp = path.with_suffix(path.suffix + '.part')
        url = f'https://github.com/{repo}/releases/download/{tag}/{asset["file"]}'
        # GitHub CLI reuses authentication on build machines without writing keys.
        if shutil.which('gh'):
            run('gh', 'release', 'download', tag, '-R', repo, '-p', asset['file'], '-D', cache, '--clobber')
        else:
            with urllib.request.urlopen(url, timeout=120) as src, open(tmp, 'wb') as dst:
                shutil.copyfileobj(src, dst)
            tmp.replace(path)
    if digest(path) != asset['sha256']:
        raise RuntimeError(f'archive_hash_mismatch: {asset["file"]}')
    return path

def one_zip_file(archive, leaf, target):
    with zipfile.ZipFile(archive) as z:
        entries = [n for n in z.namelist() if n.rsplit('/', 1)[-1] == leaf]
        if len(entries) != 1:
            raise RuntimeError(f'ambiguous_archive_member: {leaf}')
        target.write_bytes(z.read(entries[0]))
    target.chmod(0o755)

def app_info(path, name, binary, identifier):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(plistlib.dumps({'CFBundleIdentifier': identifier,
        'CFBundleName': name, 'CFBundleExecutable': binary,
        'CFBundleShortVersionString': '0.2.0', 'CFBundleVersion': '3',
        'CFBundlePackageType': 'APPL', 'LSUIElement': True,
        'LSMinimumSystemVersion': '14.0',
        'NSInputMonitoringUsageDescription': 'Detect human activity without reading key contents.'}))

def build(go, target, arch, output, package, gui=False):
    env = dict(os.environ, GOOS=target, GOARCH=arch, CGO_ENABLED='0')
    ld = '-s -w' + (' -H=windowsgui' if gui and target == 'windows' else '')
    run(go, 'build', '-trimpath', '-buildvcs=false', '-ldflags', ld, '-o', output, package, env=env)

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--os', choices=['windows', 'darwin'], required=True)
    parser.add_argument('--arch', choices=['amd64', 'arm64'], required=True)
    parser.add_argument('--go', default='go')
    parser.add_argument('--output', default=f'dist/v{VERSION}')
    args = parser.parse_args()
    out = (ROOT / args.output).resolve(); out.mkdir(parents=True, exist_ok=True)
    cache = ROOT / '.tools' / 'release-cache'; cache.mkdir(parents=True, exist_ok=True)
    if args.os == 'darwin' and platform.system() != 'Darwin':
        raise RuntimeError('Mac packaging needs a Mac build runner; users need no build tools')
    if args.os == 'windows' and args.arch != 'amd64':
        raise RuntimeError('Windows arm64 is not an accepted target')
    with tempfile.TemporaryDirectory(prefix='dotcontrol-build-') as temp:
        temp = Path(temp); payload = temp / 'payload'; payload.mkdir()
        suffix = '.exe' if args.os == 'windows' else ''
        build(args.go, args.os, args.arch, payload / ('dotcontrol'+suffix), './cmd/dotcontrol', gui=True)
        driver_key = 'windows-x86_64' if args.os == 'windows' else 'macos-' + ('arm64' if args.arch == 'arm64' else 'x86_64')
        cua = download(LOCK['cua_driver']['assets'][driver_key], 'trycua/cua', LOCK['cua_driver']['tag'], cache)
        tunnel_key = 'windows_amd64' if args.os == 'windows' else 'macos_' + args.arch
        tunnel = download(LOCK['tunnel_client'][tunnel_key], 'openai/tunnel-client', 'v0.0.16', cache)
        one_zip_file(tunnel, 'tunnel-client'+suffix, payload / ('tunnel-client'+suffix))
        if args.os == 'windows':
            # UIA runs in its upstream helper. Shipping only the main binary
            # reproduces driver_unavailable on clean Windows machines.
            for leaf in ['cua-driver.exe', 'cua-driver-uia.exe', 'cua-cursor-theme.exe',
                         'cua_driver_sdk.dll', 'cua_driver_node_runtime.node', 'cua_driver_abi.h']:
                one_zip_file(cua, leaf, payload / leaf)
        else:
            # Preserve upstream signed bundle symlinks and metadata in its exact
            # checksum-pinned archive, not in the regular-file payload manifest.
            shutil.copy2(cua, payload / 'cua-driver.tar.gz')
            extract = temp / 'cua'; extract.mkdir()
            with tarfile.open(cua) as t:
                t.extractall(extract, filter='data')
            apps = list(extract.glob('*/CuaDriver.app'))
            if len(apps) != 1: raise RuntimeError('missing_CuaDriver_app')
            run('codesign', '--verify', '--deep', '--strict', '-R',
                '=anchor apple generic and certificate leaf[subject.OU] = "YCK386LBJ7"', apps[0])
            for app, source, binary, identifier in [
                ('DotControlActivity', 'Activity.swift', 'DotControlActivity', 'org.dotcontrol.activity'),
                ('DotControlMenu', 'Menu.swift', 'DotControlMenu', 'org.dotcontrol.menu')]:
                folder = payload / (app+'.app') / 'Contents'
                (folder/'MacOS').mkdir(parents=True)
                app_info(folder/'Info.plist', app, binary, identifier)
                native_arch = 'arm64' if args.arch == 'arm64' else 'x86_64'
                run('xcrun', 'swiftc', '-O', '-target', native_arch+'-apple-macos14.0', '-framework', 'AppKit', '-framework', 'CoreGraphics', ROOT/'native/macos'/source, '-o', folder/'MacOS'/binary)
                # Only our helper gets an ad-hoc identity; NEVER sign CuaDriver.
                run('codesign', '--force', '--sign', '-', '--identifier', identifier, folder.parent)
        shutil.copytree(ROOT/'third_party/licenses', payload/'licenses')
        for name in ['LICENSE', 'THIRD_PARTY_NOTICES.md']:
            shutil.copy2(ROOT/name, payload/name)
        for archive in [cua,tunnel]:
            if zipfile.is_zipfile(archive):
                with zipfile.ZipFile(archive) as z:
                    for n in z.namelist():
                        if not n.endswith('/') and any(x in n.lower() for x in ['license','notice']):
                            (payload/'licenses'/(archive.stem+'-'+Path(n).name)).write_bytes(z.read(n))
            elif tarfile.is_tarfile(archive):
                with tarfile.open(archive) as t:
                    for member in t.getmembers():
                        if member.isfile() and any(x in member.name.lower() for x in ['license','notice']):
                            content=t.extractfile(member)
                            if content is not None:
                                (payload/'licenses'/(archive.stem+'-'+Path(member.name).name)).write_bytes(content.read())
        files=[]
        for p in sorted(payload.rglob('*')):
            if p.is_symlink(): raise RuntimeError('payload_symlink')
            if p.is_file(): files.append({'path':p.relative_to(payload).as_posix(),'sha256':digest(p),'mode':p.stat().st_mode & 0o777 or 0o644})
        (payload/'manifest.json').write_text(json.dumps({'version':VERSION,'os':args.os,'arch':args.arch,'files':files},indent=2),encoding='utf-8')
        embedded = ROOT/'cmd/dotcontrol-setup/assets/payload.zip'
        try:
            with zipfile.ZipFile(embedded,'w',zipfile.ZIP_DEFLATED,compresslevel=6) as z:
                for p in sorted(payload.rglob('*')):
                    if p.is_file(): z.write(p,p.relative_to(payload).as_posix())
            if args.os=='windows':
                artifact=out/f'DotControl-{VERSION}-windows-amd64-setup.exe'
                build(args.go,args.os,args.arch,artifact,'./cmd/dotcontrol-setup',True)
            else:
                image=temp/'image';contents=image/'DotControl Setup.app'/'Contents';(contents/'MacOS').mkdir(parents=True)
                app_info(contents/'Info.plist','DotControl Setup','DotControlSetup','org.dotcontrol.setup')
                build(args.go,args.os,args.arch,contents/'MacOS/DotControlSetup','./cmd/dotcontrol-setup')
                artifact=out/f'DotControl-{VERSION}-macos-{args.arch}.dmg'
                run('hdiutil','create','-volname','DotControl Setup','-srcfolder',image,'-ov','-format','UDZO',artifact)
            (out/(artifact.name+'.sha256')).write_text(digest(artifact)+'  '+artifact.name+'\n')
            print(json.dumps({'artifact':str(artifact),'sha256':digest(artifact),'bytes':artifact.stat().st_size,'desktop_acceptance':'not_verified_for_these_bytes'}))
        finally:
            embedded.unlink(missing_ok=True)

if __name__=='__main__': main()
