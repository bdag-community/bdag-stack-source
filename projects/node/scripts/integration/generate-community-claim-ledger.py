#!/usr/bin/env python3
"""Generate an exhaustive first-pass disposition ledger for a source history."""

from __future__ import annotations

import argparse
import csv
import subprocess
from pathlib import Path


def git(*args: str) -> str:
    return subprocess.check_output(["git", *args], text=True).strip()


def changed_paths(commit: str) -> list[str]:
    output = git(
        "diff-tree",
        "--no-commit-id",
        "--name-only",
        "-r",
        "--root",
        commit,
    )
    return [line for line in output.splitlines() if line]


def is_test_path(path: str) -> bool:
    return (
        path.endswith("_test.go")
        or "/testdata/" in path
        or path.startswith("testdata/")
        or Path(path).name.startswith("test_")
    )


def is_doc_path(path: str) -> bool:
    return path.startswith("docs/") or path.endswith(".md")


def is_tooling_path(path: str) -> bool:
    return path.startswith((".github/", "scripts/ci/", "scripts/system51/")) or path in {
        ".gitleaks.toml",
        ".golangci.yml",
        "sonar-project.properties",
    }


def change_type(paths: list[str], parent_count: int) -> str:
    if parent_count > 1:
        return "merge_context"
    if paths and all(is_doc_path(path) for path in paths):
        return "documentation_only"
    if paths and all(is_test_path(path) or is_doc_path(path) for path in paths):
        return "tests_and_docs"
    if paths and all(
        is_test_path(path) or is_doc_path(path) or is_tooling_path(path)
        for path in paths
    ):
        return "test_tooling"
    if any(is_test_path(path) or is_doc_path(path) or is_tooling_path(path) for path in paths):
        return "mixed"
    return "product"


def domains(paths: list[str]) -> list[str]:
    rules = {
        "chain_identity": ("params/", "genesis", "chainconfig", "hardfork"),
        "consensus": ("core/blockchain/", "core/types/", "consensus", "meerdag"),
        "network_sync": ("p2p/", "wire/", "sync", "crawler", "protocol"),
        "storage_recovery": ("database", "rawdb", "snapshot", "freezer", "recovery"),
        "mining_accounting": ("mining", "bdagpool", "staking", "governance", "subsidy"),
        "rpc_contracts": ("rpc/", "gateway"),
        "security_dependencies": ("go.mod", "go.sum", "crypto", "auth", "tls"),
        "ci_build_docs": (".github/", "scripts/", "Dockerfile", "Makefile", "docs/"),
    }
    matches: list[str] = []
    lowered = [path.lower() for path in paths]
    for domain, needles in rules.items():
        if any(any(needle.lower() in path for needle in needles) for path in lowered):
            matches.append(domain)
    return matches or ["other"]


def disposition(
    patch_relation: str, kind: str, classified_domains: list[str]
) -> str:
    if patch_relation == "equivalent_on_target":
        return "already_present"
    if kind == "merge_context":
        return "content_tracked_by_child_commits"
    if "chain_identity" in classified_domains:
        return "protocol_decision_required"
    if kind in {"documentation_only", "tests_and_docs", "test_tooling"}:
        return "portable_review_required"
    return "semantic_review_required"


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--target", required=True)
    parser.add_argument("--source", required=True)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()

    merge_base = git("merge-base", args.target, args.source)
    cherry_relations: dict[str, str] = {}
    for line in git("cherry", args.target, args.source).splitlines():
        marker, commit = line.split(maxsplit=1)
        cherry_relations[commit] = (
            "equivalent_on_target" if marker == "-" else "unique_to_source"
        )

    commits = git("rev-list", "--reverse", args.source, f"^{merge_base}").splitlines()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("w", newline="", encoding="utf-8") as handle:
        writer = csv.DictWriter(
            handle,
            lineterminator="\n",
            fieldnames=[
                "source_commit",
                "parent_count",
                "patch_relation",
                "change_type",
                "domains",
                "initial_disposition",
                "subject",
                "paths",
            ],
        )
        writer.writeheader()
        for commit in commits:
            parent_count = len(git("show", "-s", "--format=%P", commit).split())
            paths = changed_paths(commit)
            relation = cherry_relations.get(commit, "merge_commit")
            kind = change_type(paths, parent_count)
            classified_domains = domains(paths)
            writer.writerow(
                {
                    "source_commit": commit,
                    "parent_count": parent_count,
                    "patch_relation": relation,
                    "change_type": kind,
                    "domains": ";".join(classified_domains),
                    "initial_disposition": disposition(
                        relation, kind, classified_domains
                    ),
                    "subject": git("show", "-s", "--format=%s", commit),
                    "paths": ";".join(paths),
                }
            )

    print(f"wrote {len(commits)} source commits from merge base {merge_base}")


if __name__ == "__main__":
    main()
