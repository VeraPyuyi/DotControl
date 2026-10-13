import pathlib
import tempfile
import unittest
from verify_release_payload import resolve_artifact


class ArtifactDiscovery(unittest.TestCase):
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
