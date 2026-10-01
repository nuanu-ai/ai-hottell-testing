#!/usr/bin/env python3
"""Snapshot local skill metadata and validate/publish a reviewed private report.

An analytical agent authors the candidate from Deep reports and this inventory.
The command never installs skills or labels old sessions as a proven D19 miss.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO))
from analytics.v2_skills import validate_skill_report  # noqa: E402
from local.v2_worker import atomic_json, private_dir  # noqa: E402


def now() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def metadata(path: Path) -> dict | None:
    content = path.read_text(encoding="utf-8")
    if not content.startswith("---\n"):
        return None
    front = content.split("---\n", 2)[1]
    fields = {}
    description_block = False
    for line in front.splitlines():
        if description_block:
            if line.startswith((" ", "\t")):
                part = line.strip()
                if part:
                    fields["description"] = (fields.get("description", "") + " " + part).strip()
                continue
            description_block = False
        if line.startswith(("name:", "description:")):
            key, value = line.split(":", 1)
            value = value.strip().strip('"').strip("'")
            if key == "description" and value in {"|", ">", "|-", ">-"}:
                fields[key] = ""
                description_block = True
            else:
                fields[key] = value
    if not fields.get("name") or not fields.get("description"):
        return None
    return {
        "name": fields["name"],
        "description": fields["description"],
        "description_sha256": hashlib.sha256(fields["description"].encode()).hexdigest(),
        "path_class": "~/" + str(path.relative_to(Path.home().resolve())).removesuffix("/SKILL.md"),
    }


def snapshot() -> dict:
    home = Path.home().resolve()
    roots = (home / ".codex" / "skills", home / ".agents" / "skills")
    items = {}
    for root in roots:
        if not root.is_dir():
            continue
        for path in sorted(root.glob("*/SKILL.md")):
            if ".system" in path.parts or not path.is_file() or path.is_symlink():
                continue
            item = metadata(path)
            if item and item["name"] not in items:
                items[item["name"]] = item
    return {"snapshot_at": now(), "scope": "user-installed local skills; system and plugin skills excluded", "items": sorted(items.values(), key=lambda item: item["name"])}


def deep_reports(data_dir: Path) -> dict[str, dict]:
    reports = {}
    for path in (data_dir / "v2" / "deep").glob("*.json"):
        value = json.loads(path.read_text(encoding="utf-8"))
        if value.get("session_id") != path.stem:
            raise ValueError(f"deep report identity mismatch: {path.name}")
        reports[path.stem] = value
    if not reports:
        raise ValueError("no published Deep v2 reports")
    return reports


def deep_report_hashes(data_dir: Path) -> dict[str, str]:
    return {path.stem: hashlib.sha256(path.read_bytes()).hexdigest()
            for path in (data_dir / "v2" / "deep").glob("*.json")}


def verify_inventory_current(recorded: dict, current: dict) -> None:
    """A skill recommendation must match the installed skills at publication."""
    if recorded.get("scope") != current.get("scope") or recorded.get("items") != current.get("items"):
        raise ValueError("skill inventory changed since analysis; refresh before publication")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("data_dir", type=Path)
    actions = parser.add_mutually_exclusive_group(required=True)
    actions.add_argument("--snapshot-inventory", action="store_true")
    actions.add_argument("--validate", type=Path, metavar="CANDIDATE")
    actions.add_argument("--publish", type=Path, metavar="CANDIDATE")
    args = parser.parse_args()
    if not args.data_dir.is_absolute():
        parser.error("data_dir must be absolute")
    data_dir = args.data_dir.resolve()
    if args.snapshot_inventory:
        target = data_dir / "v2" / "skill-inventory.json"
        atomic_json(target, snapshot())
        print(f"snapshotted {len(json.loads(target.read_text())['items'])} local skills")
        return
    candidate = args.validate or args.publish
    report = json.loads(candidate.read_text(encoding="utf-8"))
    inventory = json.loads((data_dir / "v2" / "skill-inventory.json").read_text(encoding="utf-8"))
    reports = deep_reports(data_dir)
    validate_skill_report(report, reports, inventory)
    verify_inventory_current(inventory, snapshot())
    hashes = deep_report_hashes(data_dir)
    for entry in report["corpus"]:
        if entry["deep_report_sha256"] != hashes[entry["session_id"]]:
            raise ValueError("corpus: Deep report version mismatch")
    if args.publish:
        destination = data_dir / "v2" / "skill-opportunities.json"
        private_dir(destination.parent)
        if not destination.exists() or json.loads(destination.read_text(encoding="utf-8")) != report:
            if destination.exists():
                history = data_dir / "v2" / "history"
                private_dir(history)
                backup = history / f"skill-opportunities-before-{time.time_ns()}.json"
                shutil.copyfile(destination, backup)
                os.chmod(backup, 0o600)
            atomic_json(destination, report)
        request_path = data_dir / "v2" / "skill-analysis-request.json"
        if request_path.is_file():
            request = json.loads(request_path.read_text(encoding="utf-8"))
            if request.get("status") == "completed" and request.get("candidate") == str(candidate):
                request.update(status="published", published_at=now())
                atomic_json(request_path, request)
        print(f"published {len(report['opportunities'])} private skill opportunities")
    else:
        print(f"validated {len(report['opportunities'])} skill opportunities")


if __name__ == "__main__":
    main()
