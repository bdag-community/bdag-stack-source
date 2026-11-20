#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Repackage an existing runtime ZIP and hash the final bytes it contains."""

import argparse
import hashlib
import json
import pathlib
import re
import zipfile

BINARIES = ("blockdag-node", "nodeworker", "mining-pool", "poolworker",
            "pool-payer", "pool-wallet-init", "pool-schema-init",
            "dashboard-api", "dashboard", "node-dashboard")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", required=True, type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--source-name", required=True)
    parser.add_argument("--source-sha256", required=True)
    parser.add_argument("--subjects", required=True, type=pathlib.Path)
    parser.add_argument("--node-binary", type=pathlib.Path)
    parser.add_argument("--nodeworker-binary", type=pathlib.Path)
    args = parser.parse_args()
    if bool(args.node_binary) != bool(args.nodeworker_binary):
        parser.error("both replacement node binaries are required")
    overrides = {"bin/blockdag-node": args.node_binary, "bin/nodeworker": args.nodeworker_binary} if args.node_binary else {}
    for path in overrides.values():
        if not path.is_file():
            parser.error(f"replacement binary is not a file: {path}")
    if not re.fullmatch(r"stack-v[0-9]+\.[0-9]+\.[0-9]+", args.version):
        parser.error("invalid release version")
    if not re.fullmatch(r"[0-9a-f]{64}", args.source_sha256):
        parser.error("invalid source SHA-256")
    if pathlib.PurePath(args.source_name).name != args.source_name:
        parser.error("source name must be a filename")
    if args.output.exists() or pathlib.Path(str(args.output) + ".sha256").exists():
        parser.error("output already exists")
    subjects = json.loads(args.subjects.read_text())
    subjects_bytes = (json.dumps(subjects, sort_keys=True, indent=2) + "\n").encode()
    source_text = (f"Corresponding source: {args.source_name}\n"
                   f"SHA-256: {args.source_sha256}\n").encode()
    source_root = args.input.stem + "/"
    dest_root = args.output.stem + "/"
    checksums = {}
    replaced = {"checksums.txt", "SOURCE.txt", "release-source-subjects.json",
                "version.txt", "release-payload.env"}
    with zipfile.ZipFile(args.input) as old, zipfile.ZipFile(args.output, "x", allowZip64=True) as out:
        names = old.namelist()
        if len(names) != len(set(names)):
            raise ValueError("duplicate input archive entries")
        required = replaced | {"bin/" + name for name in BINARIES}
        if not required.issubset({name.removeprefix(source_root) for name in names if name.startswith(source_root)}):
            raise ValueError("input archive missing required files")
        for info in old.infolist():
            if not info.filename.startswith(source_root):
                raise ValueError("unexpected input archive root")
            rel = info.filename[len(source_root):]
            if not rel:
                continue
            path = pathlib.PurePosixPath(rel)
            if path.is_absolute() or ".." in path.parts:
                raise ValueError("unsafe input archive path")
            if rel in replaced:
                continue
            if rel.startswith("bin/") and not info.is_dir() and rel not in {"bin/" + n for n in BINARIES}:
                raise ValueError("unexpected binary in release")
            clone = zipfile.ZipInfo(dest_root + rel, date_time=info.date_time)
            clone.compress_type = info.compress_type
            clone.external_attr = info.external_attr
            clone.create_system = info.create_system
            h = hashlib.sha256() if rel.startswith("bin/") and not info.is_dir() else None
            with (overrides[rel].open("rb") if rel in overrides else old.open(info)) as source, out.open(clone, "w") as target:
                while chunk := source.read(1024 * 1024):
                    target.write(chunk)
                    if h:
                        h.update(chunk)
            if h:
                checksums[rel] = h.hexdigest()
        if set(checksums) != {"bin/" + name for name in BINARIES}:
            raise ValueError("binary set differs from expected")
        env = old.read(source_root + "release-payload.env").decode().splitlines()
        keys = {"BDAG_RELEASE_VERSION", "BDAG_STACK_RELEASE_TAG"}
        found = set()
        for i, line in enumerate(env):
            key, sep, _ = line.partition("=")
            if sep and key in keys:
                if key in found:
                    raise ValueError("duplicate version setting")
                env[i] = key + "=" + args.version
                found.add(key)
        if found != keys:
            raise ValueError("release version settings missing")
        out.writestr(dest_root + "release-payload.env", "\n".join(env) + "\n")
        out.writestr(dest_root + "version.txt", args.version + "\n")
        out.writestr(dest_root + "SOURCE.txt", source_text)
        out.writestr(dest_root + "release-source-subjects.json", subjects_bytes)
        out.writestr(dest_root + "checksums.txt", "".join(
            checksums["bin/" + name] + "  bin/" + name + "\n" for name in BINARIES))
    with args.output.open("rb") as stream:
        digest = hashlib.file_digest(stream, "sha256").hexdigest()
    pathlib.Path(str(args.output) + ".sha256").write_text(digest + "  " + args.output.name + "\n")
    print(args.output)


if __name__ == "__main__":
    main()
