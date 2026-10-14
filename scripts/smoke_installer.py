"""Isolated install/upgrade checks using the actual embedded release payload.

Never starts an agent, enables login startup, or writes system credentials/apps.
The old version is an explicitly synthetic owned-generation fixture.
"""
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile


def read(path):
    return json.loads(path.read_text(encoding='utf-8'))


def write(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, separators=(',', ':')), encoding='utf-8')


def manifest_hash(m):
    return hashlib.sha256(json.dumps(m, ensure_ascii=False, separators=(',', ':')).encode()).hexdigest()


def command(*args, success=True):
    p = subprocess.run([str(a) for a in args], capture_output=True, text=True,
                       encoding='utf-8', timeout=150)
    if success and p.returncode:
        raise RuntimeError('isolated_install_failed: ' + p.stderr[:800])
    if not success and not p.returncode:
        raise RuntimeError('invalid_upgrade_succeeded')
    return p


def run(setup):
    checks = []
    with tempfile.TemporaryDirectory(prefix='DotControl-isolated-') as tmp:
        base = Path(tmp).resolve()
        root, state, bundle = [base / p for p in ['程序 root', '设备 state', 'payload']]
        state.mkdir()
        original = {'device_id': 'dev_installer_fixture', 'name': '安装测试',
                    'listen': '127.0.0.1:19377', 'driver_path': 'unchanged-old-driver',
                    'idle_seconds': 60, 'terminal_only': True, 'history_enabled': True,
                    'tunnel_id': 'tunnel_fixture_not_connected', 'custom_future_field': 'retain'}
        write(state / 'config.json', original)
        retained = {'history_access.json': b'{"mode":"allowlist","revision":2,"allowed_thread_ids":["fixture"]}',
                    'actions.fixture': b'old unknown receipt\n', 'revoked': b'revoked\n'}
        for name, data in retained.items():
            (state / name).write_bytes(data)
        install_args = ['--install', '--state', state, '--program-root', root, '--no-start']
        command(setup, *install_args)
        current = read(root / 'current.json')
        checks.append('actual_embedded_install_without_runtime_or_credentials')
        command(setup, *install_args)
        assert read(root / 'current.json') == current
        checks.append('repeat_install')
        command(setup, '--extract-payload', bundle)
        m = read(bundle / 'manifest.json')
        # Go canonicalizes manifest inventory by case-sensitive relative path;
        # pathlib's Windows sorting uses case-folding instead.
        m['files'].sort(key=lambda spec: spec['path'])
        agent = bundle / ('dotcontrol.exe' if m['os'] == 'windows' else 'dotcontrol')

        # Real installer rejects a manifest claiming a different binary version.
        broken = base / 'failed bundle'
        shutil.copytree(bundle, broken)
        failed_manifest = dict(m, version='0.2.0-fixture-failed')
        write(broken / 'manifest.json', failed_manifest)
        before_config = (state / 'config.json').read_bytes()
        failed = command(agent, '--state', state, 'repair', '--bundle', broken,
                         '--program-root', root, '--no-start', success=False)
        assert 'agent_binary_health_failed' in failed.stderr, failed.stderr[:800]
        transaction = read(root / 'transaction.json')
        assert transaction['phase'] == 'failed', transaction
        assert transaction['target']['version'] == failed_manifest['version'], transaction
        assert transaction.get('recovery_needed', False) is False, transaction
        assert 'agent_binary_health_failed' in transaction.get('error', ''), transaction
        assert (Path(transaction['target']['dir']) / 'manifest.json').is_file()
        assert read(root / 'current.json') == current
        assert (state / 'config.json').read_bytes() == before_config
        checks.append('failed_health_restores_previous_pointer_and_config')

        # Convert only the disposable installed generation to a synthetic old one.
        old = root / 'versions' / 'fixture-old'
        Path(current['dir']).rename(old)
        old_m = dict(m, version='0.2.0-fixture-old')
        write(old / 'manifest.json', old_m)
        digest = manifest_hash(old_m)
        old_current = dict(current, version=old_m['version'], dir=str(old), manifest_sha256=digest)
        write(root / 'current.json', old_current)
        inventory = read(root / '.dotcontrol-install.json')
        inventory['versions'] = [{'dir': old.name, 'hash': digest, 'manifest': old_m}]
        write(root / '.dotcontrol-install.json', inventory)
        note = old / 'user-note.txt'
        note.write_text('retain user file', encoding='utf-8')

        changed = old / old_m['files'][-1]['path']
        expected = changed.read_bytes()
        changed.write_bytes(expected + b'user change')
        p = command(setup, *install_args)
        result = json.loads(p.stdout)
        assert result.get('cleanup_pending') is True, result
        new_current = read(root / 'current.json')
        assert new_current['version'] == m['version']
        assert changed.read_bytes() == expected + b'user change'
        checks.append('changed_owned_file_preserved_new_version_retained')
        changed.write_bytes(expected)
        command(setup, *install_args)
        assert read(root / 'current.json') == new_current
        assert not (old / 'manifest.json').exists(), read(root / 'transaction.json')
        assert note.read_text(encoding='utf-8') == 'retain user file'
        for spec in old_m['files']:
            assert not (old / spec['path']).exists(), spec['path']
        checks.append('cleanup_retry_removes_only_owned_old_files')

        config = read(state / 'config.json')
        for key, value in original.items():
            if key != 'driver_path':
                assert config[key] == value, key
        for name, value in retained.items():
            assert (state / name).read_bytes() == value, name
        checks.append('identity_tunnel_shared_scope_unknown_config_and_records_preserved')

        # Use the extracted binary, never the installed executable being removed.
        # --no-start must also isolate uninstall from OS startup and credentials.
        command(agent, '--state', state, 'uninstall', '--program-root', root, '--no-start')
        assert not (root / 'current.json').exists()
        assert read(root / '.dotcontrol-install.json').get('retained_state')
        command(setup, *install_args)
        current = read(root / 'current.json')
        assert current['version'] == m['version']
        checks.append('retained_state_uninstall_and_reinstall_without_legacy_binary')

        missing = root / 'versions' / 'fixture-missing'
        Path(current['dir']).rename(missing)
        missing_m = dict(m, version='0.2.0-fixture-missing')
        write(missing / 'manifest.json', missing_m)
        missing_digest = manifest_hash(missing_m)
        write(root / 'current.json', dict(current, version=missing_m['version'],
                                         dir=str(missing), manifest_sha256=missing_digest))
        inventory = read(root / '.dotcontrol-install.json')
        inventory['versions'] = [{'dir': missing.name, 'hash': missing_digest, 'manifest': missing_m}]
        write(root / '.dotcontrol-install.json', inventory)
        missing_exe = missing / agent.name
        assert missing_exe.resolve().is_relative_to(base)
        missing_exe.unlink()
        command(setup, *install_args)
        assert read(root / 'current.json')['version'] == m['version']
        checks.append('managed_missing_old_executable_repaired')
        for name, value in retained.items():
            assert (state / name).read_bytes() == value, name
    return {'scope': 'isolated_no_start_synthetic_old_generation', 'checks': checks,
            'passed': len(checks), 'desktop_acceptance': 'not_tested'}
