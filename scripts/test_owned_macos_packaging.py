"""Portable tests for the owned-only signing boundary and manifest ordering."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import package_release as package


class OwnedPackagingTests(unittest.TestCase):
    def test_cua_and_unknown_targets_cannot_be_resigned(self):
        with patch.object(package, 'run') as run:
            for name, identifier in [('CuaDriver.app', 'org.dotcontrol.menu'),
                                     ('dotcontrol', 'unknown')]:
                with self.assertRaisesRegex(RuntimeError, 'not_an_owned'):
                    package.sign_owned_macos(Path(name), identifier)
            run.assert_not_called()

    def test_manifest_hashes_signed_bytes(self):
        with tempfile.TemporaryDirectory() as tmp:
            payload = Path(tmp)
            agent = payload/'dotcontrol'
            agent.write_bytes(b'unsigned')
            def sign(*args):
                if '--sign' in args:
                    Path(args[-1]).write_bytes(b'new signed bytes')
            with patch.object(package, 'run', side_effect=sign):
                package.sign_owned_macos(agent, 'org.dotcontrol.agent')
            package.write_manifest(payload, 'darwin', 'arm64')
            data = json.loads((payload/'manifest.json').read_text())
            self.assertEqual(data['files'][0]['sha256'], package.digest(agent))
            self.assertEqual(agent.read_bytes(), b'new signed bytes')


if __name__ == '__main__':
    unittest.main()
