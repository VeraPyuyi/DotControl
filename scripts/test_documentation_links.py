import re
import unittest
from pathlib import Path
from urllib.parse import unquote, urlsplit


class DocumentationLinkTests(unittest.TestCase):
    def test_readme_local_links_resolve(self):
        root = Path(__file__).resolve().parents[1]
        readme = (root / 'README.md').read_text(encoding='utf-8')
        local_links = [urlsplit(target) for target in re.findall(r'\[[^\]]+\]\(([^\s)]+)\)', readme)]
        local_links = [link for link in local_links if not link.scheme and link.path]
        self.assertTrue(local_links, 'README contains no local documentation links')
        for link in local_links:
            with self.subTest(path=link.path):
                self.assertTrue((root / unquote(link.path)).is_file(), f'Broken README link: {link.path}')


if __name__ == '__main__':
    unittest.main()
