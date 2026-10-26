#!/usr/bin/env python3
"""Package an already verified amd64 root; no source/build policy lives here."""
import argparse
import copy
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile


MAX_MEMBERS = 250_000
MAX_BYTES = 16 * 1024**3
MAX_FILE = 4 * 1024**3
FORBIDDEN = {"bootstrap-bin", "work", "secrets", ".git"}
REQUIRED = ("amd64/9pc64", "amd64/bin/rc", "amd64/lib/libc.a")
REFERENCE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._:/@-]*\Z")


def require(ok, message):
    if not ok:
        raise ValueError(message)


def sha256(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def unique(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate JSON field")
        result[key] = value
    return result


def read_json(path):
    require(not path.is_symlink() and path.is_file() and path.stat().st_size <= 128 * 1024**2,
            "missing or oversized JSON metadata")
    return json.loads(path.read_text(), object_pairs_hook=unique)


def provenance(info, base):
    require(isinstance(info, dict), "provenance must be a JSON object")
    for key, pattern in (("source_commit", r"(?:[0-9a-f]{40}|[0-9a-f]{64})"),
                         ("source_tree", r"(?:[0-9a-f]{40}|[0-9a-f]{64})"),
                         ("recipe_sha256", r"[0-9a-f]{64}"),
                         ("build_log_sha256", r"[0-9a-f]{64}")):
        require(isinstance(info.get(key), str) and re.fullmatch(pattern, info[key]),
                "invalid provenance field: " + key)
    require(info.get("bootstrap_image") == base, "provenance bootstrap_image mismatch")


def relative(name):
    require(isinstance(name, str) and len(name) <= 4096 and
            not any(ord(c) < 32 or ord(c) == 127 for c in name),
            "invalid archive/manifest path")
    require(not name or all(p not in ("", ".", "..") for p in name.split("/")),
            "noncanonical archive/manifest path")
    return name


def ownership(manifest, identities, agent=None):
    require(manifest.get("arch") == "amd64", "seed architecture is not amd64")
    ids = identities.get("linux_ids", {})
    require("sys" in ids and all(
        isinstance(n, str) and re.fullmatch(r"[A-Za-z0-9_.-]{1,100}", n) and
        type(v) is int and 0 < v < 2**31 for n, v in ids.items()),
        "invalid seed identities")
    owners = {}
    for entry in manifest["entries"]:
        name = relative(entry["path"])
        require(name not in owners, "duplicate seed manifest path")
        require(entry["type"] in ("file", "dir"), "invalid seed manifest type")
        uid, gid = entry["uid"], entry["gid"]
        require(uid in ids and gid in ids and entry["linux_uid"] == ids[uid] and
                entry["linux_gid"] == ids[gid], "seed ownership metadata mismatch")
        owners[name] = (uid, gid, ids[uid], ids[gid])
    require("" in owners, "seed manifest lacks root")
    # A present interface must agree with the seed manifest, never silently
    # fall back to a legacy image when its metadata is broken.
    if agent is not None:
        kernel = next((e for e in manifest["entries"] if e["path"] == REQUIRED[0]), {})
        require(agent["schema_version"] == 1 and agent["interface_version"] == 1 and
                agent["payload"]["architecture"] == "amd64" and
                agent["kernel"]["path"] == "/9front/" + REQUIRED[0] and
                agent["kernel"]["sha256"] == kernel.get("sha256") and
                agent["kernel"]["size"] == kernel.get("size") and
                agent["filesystem"]["root"] == "/9front" and
                agent["runtime"]["qemu_bundled"] is False,
                "seed agent metadata mismatch")
    return owners


class HashedReader:
    def __init__(self, stream):
        self.stream = stream
        self.hash = hashlib.sha256()

    def read(self, size):
        data = self.stream.read(size)
        self.hash.update(data)
        return data


def normalize(source, destination, owners):
    """Validate without extracting; write only canonical regular files/dirs."""
    length = source.stat().st_size
    require(source.is_file() and 1024 <= length <= MAX_BYTES + MAX_MEMBERS * 8192 and
            length % 512 == 0, "invalid archive length")
    check_headers(source)
    entries, seen, total = [], {}, 0
    sys_uid = next((o[2] for o in owners.values() if o[0] == "sys"), None)
    require(sys_uid is not None, "seed manifest lacks sys ownership")
    with tarfile.open(source, "r:") as src, tarfile.open(destination, "w") as out:
        for member in src:
            require(len(entries) < MAX_MEMBERS, "too many archive members")
            name = member.name.removeprefix("./")
            if member.isdir():
                name = name.rstrip("/")
            relative(name)
            require(name == "9front" or name.startswith("9front/"),
                    "archive must contain only 9front/")
            path = name.removeprefix("9front").lstrip("/")
            require(not FORBIDDEN.intersection(path.split("/")),
                    "archive contains forbidden build/private path")
            require(path not in seen, "duplicate archive path")
            require((member.isdir() or member.isreg()) and not member.issparse() and
                    not member.linkname, "archive contains unsupported file type")
            require(type(member.size) is int and 0 <= member.size <= MAX_FILE and
                    (not member.isdir() or member.size == 0), "invalid member size")
            total += member.size
            require(total <= MAX_BYTES, "archive payload is too large")
            uid, gid, linux_uid, linux_gid = owners.get(path, ("sys", "sys", sys_uid, sys_uid))
            item = tarfile.TarInfo(name)
            item.type = tarfile.DIRTYPE if member.isdir() else tarfile.REGTYPE
            item.mode, item.mtime = member.mode & 0o777, int(member.mtime)
            item.uid, item.gid, item.uname, item.gname = linux_uid, linux_gid, uid, gid
            item.size = member.size
            entry = dict(path=path, type="dir" if member.isdir() else "file",
                         mode=item.mode, mtime=item.mtime, atime=item.mtime,
                         uid=uid, gid=gid, muid=uid, linux_uid=linux_uid,
                         linux_gid=linux_gid, size=item.size,
                         plan9_mode=item.mode | (0x80000000 if member.isdir() else 0))
            if member.isdir():
                out.addfile(item)
            else:
                with src.extractfile(member) as stream:
                    reader = HashedReader(stream)
                    out.addfile(item, reader)
                entry["sha256"] = reader.hash.hexdigest()
            entries.append(entry)
            seen[path] = entry
        # tarfile permits missing end blocks and ignores trailing garbage.
        # Native archives must have two zero blocks and only zero padding.
        with source.open("rb") as stream:
            stream.seek(src.offset)
            require(stream.read(1024) == bytes(1024), "archive lacks end blocks")
            while chunk := stream.read(1024 * 1024):
                require(not any(chunk), "archive has trailing data")
    require(seen.get("", {}).get("type") == "dir", "archive lacks 9front directory")
    for path in seen:
        if path:
            parent = path.rpartition("/")[0]
            require(seen.get(parent, {}).get("type") == "dir", "missing directory parent")
    for path in REQUIRED:
        e = seen.get(path, {})
        require(e.get("type") == "file" and e.get("size", 0) > 0,
                "missing/empty required payload: " + path)
    require(seen["amd64/bin/rc"]["mode"] & 0o111, "rc is not executable")
    return sorted(entries, key=lambda e: e["path"])


def check_headers(path):
    # Reject extended/sparse headers before tarfile can allocate their declared
    # bodies. Native 9front tar emits ordinary file/directory headers, not PAX.
    count, total = 0, 0
    with path.open("rb") as stream:
        while True:
            block = stream.read(512)
            if block == bytes(512):
                require(stream.read(512) == bytes(512), "archive lacks end blocks")
                while chunk := stream.read(1024 * 1024):
                    require(not any(chunk), "archive has trailing data")
                break
            require(len(block) == 512, "truncated archive header")
            member = tarfile.TarInfo.frombuf(block, "utf-8", "surrogateescape")
            require(member.type in (tarfile.REGTYPE, tarfile.AREGTYPE, tarfile.DIRTYPE),
                    "archive contains unsupported header type")
            count, total = count + 1, total + member.size
            require(count <= MAX_MEMBERS and 0 <= member.size <= MAX_FILE and
                    total <= MAX_BYTES, "archive exceeds bounds")
            stream.seek((member.size + 511) // 512 * 512, 1)


def kernel_hash(path):
    manifest = read_json(path)
    require(manifest["arch"] == "amd64", "seed architecture is not amd64")
    kernels = [e for e in manifest["entries"] if e["path"] == REQUIRED[0]]
    require(len(kernels) == 1 and kernels[0]["type"] == "file" and
            re.fullmatch(r"[0-9a-f]{64}", kernels[0].get("sha256", "")),
            "invalid seed kernel metadata")
    return kernels[0]["sha256"]


def write_json(path, data):
    path.write_text(json.dumps(data, sort_keys=True, indent=2) + "\n")


def metadata(share, entries, payload, build, base_id, tag, agent, input_hash):
    share.mkdir()
    digest = sha256(payload)
    revision = "source-" + build["source_commit"][:12]
    facts = dict(kind="rebuilt-source", pristine=False, build=build,
                 bootstrap_image_id=base_id, input_archive_sha256=input_hash)
    manifest = dict(arch="amd64", release=revision, official_release=False,
                    pristine=False, release_source_revision=build["source_commit"],
                    artifact=dict(file="payload.tar", format="tar", sha256=digest),
                    ownership_applied=True, provenance=facts, entries=entries)
    write_json(share / "manifest.json", manifest)
    ids = {}
    for e in entries:
        ids[e["uid"]], ids[e["gid"]] = e["linux_uid"], e["linux_gid"]
    write_json(share / "identities.json", {"linux_ids": ids})
    write_json(share / "provenance.json", facts)
    if agent is not None:
        info = copy.deepcopy(agent)
        info.update(image=tag, image_revision=revision)
        info["payload"] = dict(architecture="amd64", release=revision,
                               provenance="rebuilt-source", official_release=False,
                               source_revision=build["source_commit"],
                               source_artifact_sha256=digest)
        kernel = next(e for e in entries if e["path"] == REQUIRED[0])
        info["kernel"] = dict(path="/9front/" + REQUIRED[0], size=kernel["size"],
                              sha256=kernel["sha256"], source="rebuilt-filesystem")
        write_json(share / "agent-info.json", info)


def dockerfile(base, config, build, agent):
    """Scratch breaks ancestry: the old payload is never in a final layer."""
    lines = [
        "FROM " + base + " AS runtime", "USER 0",
        "RUN rm -rf /9front /usr/share/9front", "FROM scratch",
        "COPY --from=runtime / /", "ADD payload.tar /",
        "COPY metadata/ /usr/share/9front/",
    ]
    quote = json.dumps
    for env in config.get("Env") or []:
        key, value = env.split("=", 1)
        lines.append("ENV " + quote(key) + "=" + quote(value))
    for port in config.get("ExposedPorts") or {}:
        require(re.fullmatch(r"[0-9]+/(tcp|udp)", port), "invalid seed exposed port")
        lines.append("EXPOSE " + port)
    for key, instruction in (("WorkingDir", "WORKDIR"), ("User", "USER"),
                             ("StopSignal", "STOPSIGNAL")):
        value = config.get(key)
        if value:
            require(not any(c in value for c in "\r\n"), "invalid seed runtime config")
            lines.append(instruction + " " + value)
    for key, instruction in (("Entrypoint", "ENTRYPOINT"), ("Cmd", "CMD"),
                             ("Shell", "SHELL")):
        if config.get(key):
            lines.append(instruction + " " + quote(config[key]))
    if config.get("Volumes"):
        lines.append("VOLUME " + quote(list(config["Volumes"])))
    health = config.get("Healthcheck")
    if health:
        test = health["Test"]
        if test == ["NONE"]:
            lines.append("HEALTHCHECK NONE")
        else:
            options = "".join(" --" + name + "=" + str(health[key]) + "ns"
                              for key, name in (("Interval", "interval"),
                                                ("Timeout", "timeout"),
                                                ("StartPeriod", "start-period"),
                                                ("StartInterval", "start-interval"))
                              if health.get(key))
            if health.get("Retries"):
                options += " --retries=" + str(health["Retries"])
            require(test[0] in ("CMD", "CMD-SHELL"), "invalid seed healthcheck")
            command = quote(test[1:]) if test[0] == "CMD" else test[1]
            lines.append("HEALTHCHECK" + options + " CMD " + command)
    labels = {
        "org.opencontainers.image.title": "Rebuilt 9front amd64 9P root",
        "org.opencontainers.image.description": "Rebuilt source payload; not a pristine release",
        "org.opencontainers.image.version": "source-" + build["source_commit"][:12],
        "org.opencontainers.image.revision": build["source_commit"],
        "ai.nekko.9front.arch": "amd64",
        "ai.nekko.9front.provenance": "rebuilt-source",
    }
    if agent:
        labels["ai.nekko.9front.agent-interface"] = "1"
    for key, value in labels.items():
        lines.append("LABEL " + key + "=" + quote(value))
    return "\n".join(lines) + "\n"


def seed(base, directory):
    result = subprocess.run(["docker", "image", "inspect", base],
                            capture_output=True, text=True)
    if result.returncode:
        subprocess.run(["docker", "pull", base], check=True)
        result = subprocess.run(["docker", "image", "inspect", base],
                                check=True, capture_output=True, text=True)
    info = json.loads(result.stdout)[0]
    require(info["Os"] == "linux" and info["Architecture"] == "amd64",
            "seed runtime must be linux/amd64")
    container = subprocess.check_output(["docker", "create", info["Id"]], text=True).strip()
    try:
        subprocess.run(["docker", "cp", container + ":/usr/share/9front/.",
                        str(directory)], check=True, stdout=subprocess.DEVNULL)
    finally:
        subprocess.run(["docker", "rm", container], check=True, stdout=subprocess.DEVNULL)
    manifest = read_json(directory / "manifest.json")
    ids = read_json(directory / "identities.json")
    agent_path = directory / "agent-info.json"
    agent = read_json(agent_path) if agent_path.exists() else None
    owners = ownership(manifest, ids, agent)
    return info["Id"], info["Config"], owners, agent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("-i", required=True, dest="base")
    parser.add_argument("rootfs", type=Path)
    parser.add_argument("tag")
    parser.add_argument("provenance", type=Path)
    args = parser.parse_args()
    try:
        require(REFERENCE.fullmatch(args.base) and REFERENCE.fullmatch(args.tag),
                "invalid image reference")
        facts = read_json(args.provenance)
        provenance(facts, args.base)
        with tempfile.TemporaryDirectory(prefix="p9cloud-image-") as temp:
            work = Path(temp)
            base_id, config, owners, agent = seed(args.base, work / "seed")
            context = work / "context"
            context.mkdir()
            payload = context / "payload.tar"
            entries = normalize(args.rootfs, payload, owners)
            metadata(context / "metadata", entries, payload, facts, base_id,
                     args.tag, agent, sha256(args.rootfs))
            # BuildKit treats bare config IDs as registry image names. A
            # private temporary tag pins the inspected local ID even when
            # the caller selected a mutable tag (which may change meanwhile).
            snapshot = "p9cloud-root-runtime:" + work.name
            subprocess.run(["docker", "image", "tag", base_id, snapshot], check=True)
            try:
                (context / "Dockerfile").write_text(dockerfile(snapshot, config, facts, agent))
                subprocess.run(["docker", "build", "--platform=linux/amd64", "--network=none", "-t", args.tag,
                                str(context)], check=True)
            finally:
                subprocess.run(["docker", "image", "rm", snapshot], check=True,
                                stdout=subprocess.DEVNULL)
    except (OSError, ValueError, KeyError, TypeError, tarfile.TarError,
            subprocess.CalledProcessError) as exc:
        # Never dump caller provenance, runtime environment or credential data.
        parser.exit(1, "image: " + str(exc) + "\n")


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "kernel-hash":
        try:
            print(kernel_hash(Path(sys.argv[2])))
        except (OSError, ValueError, KeyError, TypeError) as exc:
            sys.exit("image: " + str(exc))
    else:
        main()
