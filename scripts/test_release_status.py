import unittest
from pathlib import Path
import release_status

class ReleaseStatusTests(unittest.TestCase):
    def test_record_is_deterministic_and_does_not_upgrade_acceptance(self):
        root = Path(__file__).resolve().parents[1]
        record = root / 'docs/validation/published-0.2.0-alpha.7.json'
        first = release_status.render(root, record)
        self.assertEqual(first, release_status.render(root, record))
        for value in ['3424e41', 'alpha.7', 'windows-amd64', 'macos-arm64', 'macos-amd64', 'pending', '13']:
            self.assertIn(value, first)
        self.assertNotIn('desktop: accepted', first)

    def test_api_fixture_ignores_draft(self):
        rows = release_status.api_rows([
            {'tag_name': 'v9.0.0', 'draft': True, 'assets': []},
            {'tag_name': 'v0.2.0-alpha.7', 'draft': False, 'prerelease': True,
             'assets': [{'name': 'DotControl-0.2.0-alpha.7-windows-amd64-setup.exe'}]},
        ])
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]['tag'], 'v0.2.0-alpha.7')
        self.assertEqual(rows[0]['acceptance'], 'pending')

if __name__ == '__main__':
    unittest.main()
