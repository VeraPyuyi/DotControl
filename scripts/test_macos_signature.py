"""Portable control-flow tests; these do not validate an actual macOS signature."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
BASH = (str(Path(os.environ.get("ProgramFiles", "C:/Program Files")) / "Git/bin/bash.exe")
        if os.name == "nt" else shutil.which("bash"))


def shell_path(path):
    text = Path(path).resolve().as_posix()
    return "/" + text[0].lower() + text[2:] if os.name == "nt" else text


class SignatureGateTests(unittest.TestCase):
    def run_gate(self, status):
        with tempfile.TemporaryDirectory(prefix="dotcontrol-signature-test-") as tmp:
            log = Path(tmp) / "arguments.txt"
            command = r'''
set -eu
signature_log="$2"
signature_status="$3"
codesign() {
    printf '%s\n' "$@" > "$signature_log"
    return "$signature_status"
}
source "$1"
verify_cua_macos_signature "$4"
printf 'protected-operation-reached\n'
'''
            target = "/Applications/Cua Driver $(do-not-execute).app"
            result = subprocess.run(
                [BASH, "-c", command, "signature-test",
                 shell_path(ROOT / "scripts/macos-signature.sh"), shell_path(log), str(status), target],
                text=True, capture_output=True, timeout=10,
            )
            args = log.read_text().splitlines() if log.exists() else []
            return result, args, target

    def test_valid_signature_reaches_protected_operation(self):
        result, args, target = self.run_gate(0)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("protected-operation-reached", result.stdout)
        self.assertEqual(args[-1], target)
        for flag in ("--verify", "--deep", "--strict", "-R"):
            self.assertIn(flag, args)
        self.assertIn('anchor apple generic and certificate leaf[subject.OU] = "YCK386LBJ7"', args)
        self.assertNotIn("--sign", args)

    def test_invalid_signature_stops_before_protected_operation(self):
        result, args, _ = self.run_gate(1)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("protected-operation-reached", result.stdout)
        self.assertIn("cua_signature_verification_failed", result.stderr)
        self.assertNotIn("--sign", args)

    def test_unavailable_verifier_also_stops(self):
        result, _, _ = self.run_gate(127)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("protected-operation-reached", result.stdout)
        self.assertIn("cua_signature_verification_failed", result.stderr)


if __name__ == "__main__":
    unittest.main()
