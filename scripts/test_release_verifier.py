import pathlib
import tempfile
import unittest
import plistlib
from package_release import VERSION, app_info
from verify_release_payload import resolve_artifact


class ArtifactDiscovery(unittest.TestCase):
    def test_macos_app_build_matches_release(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = pathlib.Path(tmp) / 'Info.plist'
            app_info(path, 'Fixture', 'fixture', 'org.dotcontrol.fixture')
            info = plistlib.loads(path.read_bytes())
            self.assertEqual(info['CFBundleShortVersionString'], VERSION.split('-')[0])
            self.assertEqual(info['CFBundleVersion'], VERSION.rsplit('.', 1)[-1])

    def test_nested_download_is_found(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            asset = root / 'v0.2.0-alpha.3' / 'DotControl-test.dmg'
            asset.parent.mkdir()
            asset.write_bytes(b'fixture')
            self.assertEqual(resolve_artifact(root), asset.resolve())

    def test_empty_or_ambiguous_download_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            with self.assertRaisesRegex(RuntimeError, 'exactly_one_installer_required'):
                resolve_artifact(root)
            for name in ['one.exe', 'two.dmg']:
                (root / name).write_bytes(b'fixture')
            with self.assertRaisesRegex(RuntimeError, 'exactly_one_installer_required'):
                resolve_artifact(root)


if __name__ == '__main__':
    unittest.main()
