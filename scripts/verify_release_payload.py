"""Check payload bytes, optionally testing isolated installation without startup."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
from smoke_installer import run as smoke


def resolve_artifact(path):
    path = path.resolve()
    if path.is_dir():
        artifacts = [p for p in path.rglob('*')
                     if p.is_file() and p.suffix.lower() in ('.exe', '.dmg')]
        if len(artifacts) != 1:
            raise RuntimeError('exactly_one_installer_required')
        return artifacts[0]
    if not path.is_file():
        raise RuntimeError('installer_missing')
    return path


def run(path, install_smoke=False):
    result = subprocess.run([str(path), '--verify-payload'], capture_output=True,
                            text=True, timeout=120, check=True)
    report = json.loads(result.stdout)
    if report.get('payload_verified') is not True:
        raise RuntimeError('payload_verification_failed')
    if install_smoke:
        report['isolated_install'] = smoke(path)
    return report


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('artifact', type=Path)
    parser.add_argument('--install-smoke', action='store_true')
    args = parser.parse_args()
    artifact = resolve_artifact(args.artifact)
    with artifact.open('rb') as f:
        actual = hashlib.file_digest(f, 'sha256').hexdigest()
    expected = Path(str(artifact) + '.sha256').read_text().split()[0]
    if actual != expected:
        raise RuntimeError('downloaded_artifact_hash_mismatch')
    if artifact.suffix.lower() == '.exe':
        report = run(artifact, args.install_smoke)
    elif artifact.suffix.lower() == '.dmg':
        with tempfile.TemporaryDirectory(prefix='DotControl-image-check-') as tmp:
            mount = Path(tmp) / 'mount'
            mount.mkdir()
            subprocess.run(['hdiutil', 'attach', '-readonly', '-nobrowse',
                            '-mountpoint', str(mount), str(artifact)], check=True,
                           stdout=subprocess.DEVNULL)
            try:
                report = run(mount / 'DotControl Setup.app/Contents/MacOS/DotControlSetup', args.install_smoke)
            finally:
                subprocess.run(['hdiutil', 'detach', str(mount)], check=True,
                               stdout=subprocess.DEVNULL)
    else:
        raise RuntimeError('unsupported_artifact')
    print(json.dumps({'sha256': actual, 'bytes': artifact.stat().st_size,
                      'payload': report, 'desktop_acceptance': 'not_verified'}))


if __name__ == '__main__':
    main()
