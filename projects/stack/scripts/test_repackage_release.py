# SPDX-License-Identifier: GPL-3.0-or-later
import hashlib
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest
import zipfile


SCRIPT = pathlib.Path(__file__).with_name("repackage-release.py")
BINARIES = ("blockdag-node", "nodeworker", "mining-pool", "poolworker",
            "pool-payer", "pool-wallet-init", "pool-schema-init",
            "dashboard-api", "dashboard", "node-dashboard")


class RepackageReleaseTest(unittest.TestCase):
    def test_hashes_final_binaries_instead_of_copying_stale_manifest(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = pathlib.Path(tmp)
            source = path / "old.zip"
            dest = path / "pool-stack-docker-stack-v0.1.1-linux-amd64.zip"
            subjects = path / "subjects.json"
            subjects.write_text(json.dumps({"node_commit": "abc"}))
            with zipfile.ZipFile(source, "w") as z:
                for name in BINARIES:
                    z.writestr("old/bin/" + name, ("new bytes " + name).encode())
                z.writestr("old/checksums.txt", "stale checksums\n")
                z.writestr("old/version.txt", "stack-v0.1.0\n")
                z.writestr("old/release-payload.env", "BDAG_RELEASE_VERSION=stack-v0.1.0\nBDAG_STACK_RELEASE_TAG=stack-v0.1.0\n")
                z.writestr("old/SOURCE.txt", "old source\n")
                z.writestr("old/release-source-subjects.json", "{}")
            subprocess.run([sys.executable, str(SCRIPT), "--input", str(source),
                            "--output", str(dest), "--version", "stack-v0.1.1",
                            "--source-name", "source.zip", "--source-sha256", "a" * 64,
                            "--subjects", str(subjects)], check=True, stdout=subprocess.PIPE)
            prefix = dest.stem + "/"
            with zipfile.ZipFile(dest) as z:
                lines = z.read(prefix + "checksums.txt").decode().splitlines()
                self.assertEqual(len(lines), len(BINARIES))
                for line in lines:
                    digest, name = line.split(maxsplit=1)
                    self.assertEqual(digest, hashlib.sha256(z.read(prefix + name)).hexdigest())
                self.assertEqual(z.read(prefix + "version.txt"), b"stack-v0.1.1\n")
                self.assertIn(b"BDAG_STACK_RELEASE_TAG=stack-v0.1.1\n", z.read(prefix + "release-payload.env"))
                self.assertIn(b"Corresponding source: source.zip\n", z.read(prefix + "SOURCE.txt"))
            self.assertEqual(pathlib.Path(str(dest) + ".sha256").read_text().split()[0],
                             hashlib.sha256(dest.read_bytes()).hexdigest())


if __name__ == "__main__":
    unittest.main()
