"""Exercise installer logic with a fake Mac; never writes to the real user home."""
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
BASH = shutil.which("bash") or r"C:\Program Files\Git\bin\bash.exe"


def posix(path):
    if os.name != "nt":
        return str(path)
    result = subprocess.run([BASH, "-c", 'cygpath -u "$1"', "--", str(path)],
                            check=True, capture_output=True, text=True, encoding="utf-8")
    return result.stdout.strip()


class InstallerTest(unittest.TestCase):
    def setUp(self):
        (ROOT / ".cache").mkdir(exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(prefix="mac-installer-", dir=ROOT / ".cache")
        self.folder = Path(self.temporary.name)
        self.package = self.folder / "package with spaces"
        self.package.mkdir()
        self.destination = self.folder / "Mac user's $private directory"
        self.destination.mkdir()
        self.mocks = self.folder / "mock-bin"
        self.mocks.mkdir()
        shutil.copyfile(ROOT / "scripts/install-mac.sh", self.package / "install-mac.sh")
        self.script("uname", 'if [ "$1" = -s ]; then echo Darwin; else echo "${TEST_MACHINE:-arm64}"; fi\n')
        self.script("sysctl", 'echo "${TEST_ARM_HARDWARE:-1}"\n')
        self.script("curl", 'printf "%s\\n" "${TEST_HEALTH}"\n')
        self.script("shasum", 'if command -v sha256sum >/dev/null 2>&1; then sha256sum "$3"; else /usr/bin/shasum "$@"; fi\n')
        binary = b'''#!/bin/bash
if [ "$1" = version ]; then echo "mywormhole 0.1.0"; exit 0; fi
printf '%s\n%s\n%s\n' "$MYWORMHOLE_SERVER" "$MYWORMHOLE_INVITE_TOKEN" "$1" > "$2"
'''
        sums = []
        for arch in ("arm64", "amd64"):
            name = "mywormhole-darwin-" + arch
            (self.package / name).write_bytes(binary)
            sums.append(hashlib.sha256(binary).hexdigest() + "  " + name)
        (self.package / "SHA256SUMS").write_text("\n".join(sums) + "\n", encoding="utf-8")
        self.env = dict(os.environ, SHELL="/bin/zsh", TEST_MACHINE="arm64", TEST_ARM_HARDWARE="1",
                        TEST_HEALTH='{"service":"mywormhole","protocol":1,"ready":true}')
        self.root_posix = posix(self.destination)
        self.mock_posix = posix(self.mocks)
        self.script_posix = posix(self.package / "install-mac.sh")
        self.token = "private-token-$('never-execute')-with-quotes'and\\slashes"

    def tearDown(self):
        self.temporary.cleanup()

    def script(self, name, text):
        file = self.mocks / name
        file.write_text("#!/bin/bash\n" + text, encoding="utf-8", newline="\n")
        file.chmod(0o755)

    def install(self, input_text, *arguments, expected=0):
        result = subprocess.run(
            [BASH, "-c", 'export PATH="$1:$PATH"; shift; exec /bin/bash "$@"', "--",
             self.mock_posix, self.script_posix, "--install-root", self.root_posix, *arguments],
            input=input_text.encode("utf-8"), capture_output=True, env=self.env, timeout=30)
        result.stdout = result.stdout.decode("utf-8")
        result.stderr = result.stderr.decode("utf-8")
        self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
        return result

    def test_private_config_argument_forwarding_and_idempotence(self):
        profile = self.destination / ".zshrc"
        profile.write_text("# keep existing configuration\n", encoding="utf-8")
        result = self.install("https://example.workers.dev/\n" + self.token + "\n")
        self.assertNotIn(self.token, result.stdout + result.stderr)
        self.assertEqual((self.destination / ".config/mywormhole/settings").read_text().splitlines()[1], self.token)
        self.assertNotIn(self.token, profile.read_text())
        launcher = posix(self.destination / ".local/bin/mywormhole")
        output = self.folder / "forwarded arguments.txt"
        subprocess.run([BASH, launcher, "send", posix(output)], check=True, env=self.env)
        self.assertEqual(output.read_text().splitlines(), ["https://example.workers.dev", self.token, "send"])
        self.install("\n\n")
        self.assertEqual(profile.read_text().count("# mywormhole"), 1)
        self.assertIn("# keep existing configuration", profile.read_text())
        self.assertEqual((self.destination / ".config/mywormhole/settings").read_text().splitlines()[1], self.token)
        if os.name != "nt":
            self.assertEqual((self.destination / ".config/mywormhole/settings").stat().st_mode & 0o777, 0o600)

    def test_intel_and_rosetta_selection(self):
        self.env.update(TEST_MACHINE="x86_64", TEST_ARM_HARDWARE="0")
        result = self.install(self.token + "\n", "--server", "https://example.workers.dev")
        self.assertIn("amd64", result.stdout)
        self.env["TEST_ARM_HARDWARE"] = "1"
        result = self.install("\n", "--server", "https://example.workers.dev")
        self.assertIn("arm64", result.stdout)

    def test_bad_health_preserves_existing_config(self):
        self.install(self.token + "\n", "--server", "https://example.workers.dev")
        self.env["TEST_HEALTH"] = '{"service":"mywormhole","protocol":1,"ready":false}'
        self.install("", "--server", "https://not-ready.workers.dev", expected=1)
        self.assertEqual((self.destination / ".config/mywormhole/settings").read_text().splitlines(), ["https://example.workers.dev", self.token])
        self.assertEqual(list(self.destination.rglob(".install-*")), [])

    def test_offline_flag_and_invalid_address(self):
        self.env["TEST_HEALTH"] = "unreachable"
        self.install(self.token + "\n", "--server", "https://example.workers.dev", "--skip-health-check")
        self.install("", "--server", "https://example.workers.dev/path", "--skip-health-check", expected=1)
        self.assertEqual((self.destination / ".config/mywormhole/settings").read_text().splitlines()[0], "https://example.workers.dev")

    def test_checksum_corruption_and_startup_failure_do_not_save_credentials(self):
        binary = self.package / "mywormhole-darwin-arm64"
        binary.write_bytes(binary.read_bytes() + b"# corrupted\n")
        self.install("", "--server", "https://example.workers.dev", expected=1)
        self.assertFalse((self.destination / ".config/mywormhole/settings").exists())
        (self.package / "SHA256SUMS").unlink()
        binary.write_bytes(b"#!/bin/bash\nexit 1\n")
        self.install("", "--server", "https://example.workers.dev", expected=1)
        self.assertFalse((self.destination / ".config/mywormhole/settings").exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)
