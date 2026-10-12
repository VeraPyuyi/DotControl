"""Build/sign only new owned artifacts; never install, launch, or change TCC."""
import argparse
import json
from pathlib import Path
import platform
import tempfile

import package_release as package


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--go', default='go')
    args = parser.parse_args()
    arch = 'arm64' if platform.machine() == 'arm64' else 'amd64'
    target = ('arm64' if arch == 'arm64' else 'x86_64')+'-apple-macos14.0'
    with tempfile.TemporaryDirectory(prefix='DotControl-owned-build-') as tmp:
        payload = Path(tmp)/'payload'
        payload.mkdir()
        agent = payload/'dotcontrol'
        package.build(args.go, 'darwin', arch, agent, './cmd/dotcontrol')
        package.sign_owned_macos(agent, 'org.dotcontrol.agent')
        for name, source, identifier in [
            ('DotControlActivity', 'Activity.swift', 'org.dotcontrol.activity'),
            ('DotControlMenu', 'Menu.swift', 'org.dotcontrol.menu')]:
            app = payload/(name+'.app')
            folder = app/'Contents'
            (folder/'MacOS').mkdir(parents=True)
            package.app_info(folder/'Info.plist', 'DotControl' if name == 'DotControlMenu' else name, name, identifier)
            package.run('xcrun', 'swiftc', '-O', '-target', target, '-framework', 'AppKit',
                        '-framework', 'CoreGraphics', package.ROOT/'native/macos'/source,
                        '-o', folder/'MacOS'/name)
            package.sign_owned_macos(app, identifier)
        package.write_manifest(payload, 'darwin', arch)
        manifest = json.loads((payload/'manifest.json').read_text())
        for file in manifest['files']:
            if package.digest(payload/file['path']) != file['sha256']:
                raise RuntimeError('signed_payload_hash_mismatch')
        app = Path(tmp)/'DotControl Setup.app'
        (app/'Contents/MacOS').mkdir(parents=True)
        package.app_info(app/'Contents/Info.plist', 'DotControl Setup', 'DotControlSetup', 'org.dotcontrol.setup')
        package.build(args.go, 'darwin', arch, app/'Contents/MacOS/DotControlSetup', './cmd/dotcontrol-setup')
        package.sign_owned_macos(app, 'org.dotcontrol.setup')
        print(json.dumps({'arch': arch, 'owned_identifiers_verified': list(package.OWNED_MAC_IDENTIFIERS),
            'signed_payload_hashes_verified': len(manifest['files']), 'installed_or_launched': False,
            'desktop_acceptance': 'not_verified'}))


if __name__ == '__main__':
    main()
