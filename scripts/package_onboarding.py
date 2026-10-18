"""Reproducible onboarding plugin archive containing only public source files."""
import hashlib
import json
from pathlib import Path
import zipfile


ROOT = Path(__file__).resolve().parents[1]


def main():
    plugin = ROOT / 'plugins/dotcontrol-onboarding'
    version = json.loads((plugin / 'plugin.json').read_text(encoding='utf-8'))['version']
    out = ROOT / 'dist' / ('v' + version)
    out.mkdir(parents=True, exist_ok=True)
    archive = out / f'DotControl-onboarding-{version}.zip'
    sources = [(p, p.relative_to(plugin).as_posix())
               for p in sorted(plugin.rglob('*')) if p.is_file()]
    sources += [(ROOT / p, p) for p in ['LICENSE', 'THIRD_PARTY_NOTICES.md']]
    sources += [(p, p.relative_to(ROOT).as_posix())
                for p in sorted((ROOT / 'third_party/licenses').rglob('*')) if p.is_file()]
    with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as z:
        for path, name in sources:
            if path.is_symlink():
                raise RuntimeError('plugin_symlink')
            entry = zipfile.ZipInfo(name, (2026, 1, 1, 0, 0, 0))
            entry.compress_type = zipfile.ZIP_DEFLATED
            entry.external_attr = 0o100644 << 16
            z.writestr(entry, path.read_bytes())
    sha = hashlib.sha256(archive.read_bytes()).hexdigest()
    Path(str(archive) + '.sha256').write_text(f'{sha}  {archive.name}\n', encoding='utf-8')
    print(json.dumps({'artifact': archive.name, 'sha256': sha, 'files': len(sources)}))


if __name__ == '__main__':
    main()
