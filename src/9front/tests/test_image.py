import copy
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).resolve().parents[1] / "image.py"
spec = importlib.util.spec_from_file_location("rootimage", SCRIPT)
image = importlib.util.module_from_spec(spec)
spec.loader.exec_module(image)

BASE = "example/seed@sha256:" + "a" * 64
TAG = "9front-amd64:next"
PROVENANCE = dict(source_commit="b" * 40, source_tree="c" * 40,
                  recipe_sha256="d" * 64, build_log_sha256="e" * 64,
                  bootstrap_image=BASE, stages=["libs", "commands", "kernel"])
IDS = {"linux_ids": {"sys": 110000, "glenda": 100002}}
KERNEL = b"new kernel"
OLD_HASH = hashlib.sha256(b"old kernel").hexdigest()
MANIFEST = {
    "arch": "amd64",
    "entries": [
        dict(path="", type="dir", uid="sys", gid="sys",
             linux_uid=110000, linux_gid=110000),
        dict(path="amd64/9pc64", type="file", uid="sys", gid="sys",
             linux_uid=110000, linux_gid=110000, sha256=OLD_HASH, size=10),
        dict(path="usr/glenda", type="dir", uid="glenda", gid="sys",
             linux_uid=100002, linux_gid=110000),
        dict(path="amd64/bin/deleted", type="file", uid="sys", gid="sys",
             linux_uid=110000, linux_gid=110000),
    ],
}
AGENT = {
    "schema_version": 1, "interface_version": 1,
    "image": "old:tag", "image_revision": "11952-agent1",
    "payload": {"architecture": "amd64", "release": "11952",
                "official_release": True, "provenance": "official-release"},
    "runtime": {"platform": "linux/amd64", "qemu_bundled": False},
    "filesystem": {"root": "/9front", "protocol": "9P2000",
                   "read_only_default": True, "container_port": 564,
                   "authentication": "none"},
    "kernel": {"path": "/9front/amd64/9pc64", "sha256": OLD_HASH, "size": 10},
    "boot": {"supported": True, "reason": None},
    "defaults": {"host_address": "127.0.0.1", "host_port": 9568,
                 "kernel_path": "/tmp/kernel"},
}
CONFIG = {
    "Env": ["PATH=/bin:/usr/bin", "READ_ONLY=1"],
    "Entrypoint": ["/usr/local/bin/serve-9front"],
    "Cmd": None, "WorkingDir": "/", "User": "",
    "ExposedPorts": {"564/tcp": {}},
    "Healthcheck": {"Test": ["CMD", "/usr/local/bin/9p-healthcheck"],
                    "Interval": 60000000000, "Timeout": 5000000000,
                    "StartPeriod": 5000000000, "Retries": 3},
    "Labels": {"org.opencontainers.image.version": "11952-agent1"},
}


def members():
    return [(name, None) for name in
            ("9front", "9front/amd64", "9front/amd64/bin", "9front/amd64/lib",
             "9front/usr", "9front/usr/glenda")] + [
        ("9front/amd64/9pc64", KERNEL),
        ("9front/amd64/bin/rc", b"new rc"),
        ("9front/amd64/lib/libc.a", b"new libc"),
        ("9front/usr/glenda/newfile", b"contents"),
    ]


def archive(path, entries=None):
    with tarfile.open(path, "w", format=tarfile.USTAR_FORMAT) as out:
        for name, data in entries if entries is not None else members():
            m = tarfile.TarInfo(name)
            m.uid, m.gid, m.uname, m.gname = 123, 456, "wrong", "wrong"
            m.mode, m.mtime = 0o755, 123456789
            if data is None:
                m.type = tarfile.DIRTYPE
                out.addfile(m)
            else:
                m.size = len(data)
                out.addfile(m, io.BytesIO(data))


class ImageTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)
        self.source = self.dir / "input.tar"
        archive(self.source)
        self.context = self.dir / "context"
        self.context.mkdir()
        self.owners = image.ownership(MANIFEST, IDS, AGENT)

    def normalize(self):
        return image.normalize(self.source, self.context / "payload.tar", self.owners)

    def test_normalized_content_and_ownership(self):
        entries = self.normalize()
        bypath = {e["path"]: e for e in entries}
        self.assertEqual(len(entries), len(members()))
        self.assertNotIn("amd64/bin/deleted", bypath)
        self.assertEqual(bypath["usr/glenda"]["linux_uid"], 100002)
        self.assertEqual(bypath["usr/glenda/newfile"]["linux_uid"], 110000)
        self.assertEqual(bypath["amd64/9pc64"]["sha256"],
                         hashlib.sha256(KERNEL).hexdigest())
        with tarfile.open(self.context / "payload.tar") as t:
            for m in t:
                expected = bypath[m.name.removeprefix("9front").lstrip("/")]
                self.assertEqual((m.uid, m.gid), (expected["linux_uid"], expected["linux_gid"]))
                self.assertNotEqual(m.uname, "wrong")
                self.assertEqual(m.mtime, 123456789)
            self.assertEqual(t.extractfile("9front/amd64/9pc64").read(), KERNEL)

    def test_archive_validation(self):
        bad = ["../escape", "/9front/escape", "9front/../escape",
               "9front//escape", "9front/./escape", "elsewhere",
               "9front/.git/config", "9front/et/work/log",
               "9front/secrets/nvram", "9front/bootstrap-bin/rc"]
        for name in bad:
            with self.subTest(name=name):
                archive(self.source, members() + [(name, b"x")])
                with self.assertRaises(ValueError):
                    self.normalize()

    def test_duplicate_missing_and_file_parents(self):
        bad = [
            members() + [("9front/amd64/bin/rc", b"duplicate")],
            [(n, b"not a directory" if n == "9front/amd64" else d)
             for n, d in members()],
            [(n, d) for n, d in members() if n != "9front/amd64/bin/rc"],
            [(n, d) for n, d in members() if n != "9front/amd64"],
        ]
        bad.append([(n, b"" if n == "9front/amd64/lib/libc.a" else d)
                    for n, d in members()])
        for entries in bad:
            archive(self.source, entries)
            with self.assertRaises(ValueError):
                self.normalize()

    def test_special_types(self):
        for kind in (tarfile.SYMTYPE, tarfile.LNKTYPE, tarfile.CHRTYPE,
                     tarfile.BLKTYPE, tarfile.FIFOTYPE):
            with self.subTest(kind=kind):
                archive(self.source)
                with tarfile.open(self.source, "a") as out:
                    m = tarfile.TarInfo("9front/special")
                    m.type, m.linkname = kind, "/etc/shadow"
                    out.addfile(m)
                with self.assertRaises(ValueError):
                    self.normalize()

    def test_extension_headers_and_empty_ordinary_files(self):
        with tarfile.open(self.source, "w", format=tarfile.PAX_FORMAT) as out:
            m = tarfile.TarInfo("9front/long-" + "x" * 200)
            m.size = 1
            out.addfile(m, io.BytesIO(b"x"))
        with self.assertRaisesRegex(ValueError, "header type"):
            self.normalize()
        archive(self.source, members() + [("9front/empty", b"")])
        self.assertEqual(next(e for e in self.normalize() if e["path"] == "empty")["size"], 0)

    def test_bad_truncated_and_trailing_archive(self):
        valid = self.source.read_bytes()
        for data in (b"not a tar", valid[:100], valid[:1024],
                     valid.rstrip(b"\0"), valid + b"x" * 512):
            with self.subTest(length=len(data)):
                self.source.write_bytes(data)
                with self.assertRaises((ValueError, tarfile.TarError)):
                    self.normalize()

    def test_bounds(self):
        for key, value in (("MAX_MEMBERS", 2), ("MAX_BYTES", 2), ("MAX_FILE", 2)):
            with patch.object(image, key, value):
                with self.assertRaises(ValueError):
                    self.normalize()

    def test_metadata_mismatch(self):
        cases = []
        m = copy.deepcopy(MANIFEST)
        m["arch"] = "arm64"
        cases.append((m, IDS, AGENT))
        m = copy.deepcopy(MANIFEST)
        m["entries"][1]["linux_uid"] = 0
        cases.append((m, IDS, AGENT))
        m = copy.deepcopy(MANIFEST)
        m["entries"].append(m["entries"][0])
        cases.append((m, IDS, AGENT))
        cases.append((MANIFEST, {"linux_ids": {}}, AGENT))
        a = copy.deepcopy(AGENT)
        a["kernel"]["sha256"] = "0" * 64
        cases.append((MANIFEST, IDS, a))
        for args in cases:
            with self.assertRaises(ValueError):
                image.ownership(*args)

    def test_provenance(self):
        image.provenance(PROVENANCE, BASE)
        for key in PROVENANCE:
            if key == "stages":
                continue
            bad = dict(PROVENANCE)
            bad[key] = "bad"
            with self.assertRaises(ValueError):
                image.provenance(bad, BASE)
        with self.assertRaises(ValueError):
            image.provenance(PROVENANCE, "other:seed")

    def test_kernel_hash_and_duplicate_json(self):
        path = self.dir / "manifest.json"
        path.write_text(json.dumps(MANIFEST))
        self.assertEqual(image.kernel_hash(path), OLD_HASH)
        m = copy.deepcopy(MANIFEST)
        m["entries"].append(m["entries"][1])
        path.write_text(json.dumps(m))
        with self.assertRaisesRegex(ValueError, "kernel metadata"):
            image.kernel_hash(path)
        path.write_text('{"arch":"amd64","arch":"arm64"}')
        with self.assertRaisesRegex(ValueError, "duplicate JSON"):
            image.read_json(path)

    def test_generated_metadata_legacy_and_agent(self):
        entries = self.normalize()
        for agent in (None, AGENT):
            share = self.context / ("current" if agent else "legacy")
            image.metadata(share, entries, self.context / "payload.tar",
                           PROVENANCE, "sha256:" + "f" * 64, TAG, agent,
                           image.sha256(self.source))
            manifest = json.loads((share / "manifest.json").read_text())
            self.assertFalse(manifest["pristine"])
            self.assertEqual(manifest["release_source_revision"], PROVENANCE["source_commit"])
            self.assertEqual(manifest["entries"], entries)
            self.assertEqual(json.loads((share / "provenance.json").read_text())["build"], PROVENANCE)
            self.assertEqual((share / "agent-info.json").exists(), agent is not None)
            if agent:
                info = json.loads((share / "agent-info.json").read_text())
                self.assertEqual(info["image"], TAG)
                self.assertFalse(info["payload"]["official_release"])
                self.assertEqual(info["kernel"]["sha256"], hashlib.sha256(KERNEL).hexdigest())
                self.assertEqual(info["defaults"], AGENT["defaults"])
                self.assertEqual(info["runtime"], AGENT["runtime"])

    def test_runtime_only_dockerfile(self):
        text = image.dockerfile(BASE, CONFIG, PROVENANCE, True)
        self.assertLess(text.index("RUN rm -rf /9front"), text.index("FROM scratch"))
        self.assertIn("COPY --from=runtime / /", text)
        self.assertIn("ADD payload.tar /", text)
        self.assertIn('ENTRYPOINT ["/usr/local/bin/serve-9front"]', text)
        self.assertIn('ENV "READ_ONLY"="1"', text)
        self.assertIn('org.opencontainers.image.revision="' + "b" * 40 + '"', text)
        self.assertNotIn("11952", text)
        self.assertIn("HEALTHCHECK --interval=60000000000ns", text)
        self.assertIn("EXPOSE 564/tcp", text)

    def test_cli_builds_only_the_validated_context(self):
        facts = self.dir / "provenance.json"
        facts.write_text(json.dumps(PROVENANCE))
        builds = []

        def run(command, **kwargs):
            if command[:3] in (["docker", "image", "tag"], ["docker", "image", "rm"]):
                return subprocess.CompletedProcess(command, 0)
            builds.append(command)
            context = Path(command[-1])
            self.assertEqual(command[:-1], ["docker", "build", "--platform=linux/amd64",
                                            "--network=none", "-t", TAG])
            self.assertEqual(sorted(p.name for p in context.iterdir()),
                             ["Dockerfile", "metadata", "payload.tar"])
            m = image.read_json(context / "metadata/manifest.json")
            self.assertEqual(m["artifact"]["sha256"], image.sha256(context / "payload.tar"))
            self.assertNotIn("amd64/bin/deleted", [e["path"] for e in m["entries"]])
            return subprocess.CompletedProcess(command, 0)

        args = ["image.py", "-i", BASE, str(self.source), TAG, str(facts)]
        with patch.object(sys, "argv", args), \
                patch.object(image, "seed", return_value=("sha256:" + "f" * 64,
                             CONFIG, self.owners, AGENT)), \
                patch.object(image.subprocess, "run", side_effect=run):
            image.main()
            self.assertEqual(len(builds), 1)
            self.source.write_bytes(b"broken tar")
            with patch.object(image.argparse.ArgumentParser, "exit", side_effect=SystemExit(1)):
                with self.assertRaises(SystemExit):
                    image.main()
            self.assertEqual(len(builds), 1)


if __name__ == "__main__":
    unittest.main()
