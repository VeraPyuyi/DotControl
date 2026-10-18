"""Read-only release table; local recorded release or saved GitHub API JSON.

python scripts/release_status.py
python scripts/release_status.py --api-json releases.json
Save API JSON explicitly using https://api.github.com/repos/VeraPyuyi/DotControl/releases.
No test contacts the network. A published asset never implies device acceptance.
"""
import argparse
import json
import subprocess
from pathlib import Path


def api_rows(releases):
    return [{'tag': r['tag_name'], 'assets': sorted(a['name'] for a in r.get('assets', [])),
             'channel': 'prerelease' if r.get('prerelease') else 'stable',
             'acceptance': 'pending'} for r in releases if not r.get('draft')]


def render(root, record):
    data = json.loads(record.read_text(encoding='utf-8'))
    tag = 'v' + data['version']
    sha = subprocess.check_output(['git', 'rev-parse', tag], cwd=root, text=True).strip()
    if sha != data['source_commit']:
        raise ValueError('record/tag source mismatch')
    assets = sorted(a['name'] for a in data['assets'] if a['name'].endswith(('.exe', '.dmg', '.zip')))
    return ('| Branch / Release | Source | Assets / capability | Acceptance |\n'
            '|---|---|---|---|\n'
            '| default main | a5d66d0 | historical alpha.2 | not current release |\n'
            f'| {tag} | {sha} | {", ".join(assets)}; 13 tools | published; desktop pending |\n'
            '| candidate PR #1 | 971def0 | terminal/history/background lifecycle | synthetic checks only here |\n'
            '| external | pending | Windows + Mac ARM/Intel; source Dot image; publisher signature/notarization | pending |\n')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--api-json', type=Path)
    args = p.parse_args()
    root = Path(__file__).resolve().parents[1]
    if args.api_json:
        print(json.dumps(api_rows(json.loads(args.api_json.read_text(encoding='utf-8'))), ensure_ascii=False, sort_keys=True, indent=2))
    else:
        print(render(root, root / 'docs/validation/published-0.2.0-alpha.7.json'), end='')

if __name__ == '__main__':
    main()
