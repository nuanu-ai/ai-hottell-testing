#!/usr/bin/env python3
"""Publish a new Deep report only after a recorded independent semantic review.

The worker creates a private candidate. This command checks that the review
names its exact bytes and that the frozen source still matches, then updates
the published Deep report and proposal registry. It never applies proposals.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
import tempfile
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO))
from analytics.v2_contract import REVIEW_CHECKS, validate_semantic_review, validate_v2_deep  # noqa: E402
from dashboard.promote_rebuild import validate_telemetry  # noqa: E402
from local.v2_review_index import build_index  # noqa: E402
from local.v2_source_validation import validate_source_roles  # noqa: E402
from local.v2_worker import SESSION_ID, atomic_json, now, refresh_published_registry, reject_applied_duplicates  # noqa: E402


def atomic_private_bytes(path: Path, content: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(path.parent, 0o700)
    fd, temporary = tempfile.mkstemp(prefix=".v2-publish-", suffix=".json", dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "wb") as output:
            output.write(content)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)

def publish(data_dir: Path, session_id: str, review_path: Path, registry_refresh=refresh_published_registry) -> Path:
    if not SESSION_ID.fullmatch(session_id):
        raise ValueError("invalid session id")
    candidate_path = data_dir / "v2" / "review" / f"{session_id}.json"
    technical_path = data_dir / "v2" / "review-telemetry" / session_id / "telemetry.json"
    published_descriptor_path = data_dir / "v2" / "sources" / f"{session_id}.json"
    staged_descriptor_path = data_dir / "v2" / "review-sources" / f"{session_id}.json"
    descriptor_path = staged_descriptor_path if staged_descriptor_path.is_file() else published_descriptor_path
    for private_path in (candidate_path, technical_path, descriptor_path, review_path):
        if private_path.stat().st_mode & 0o777 != 0o600:
            raise ValueError(f"private input must have 0600 permissions: {private_path}")
    candidate_bytes = candidate_path.read_bytes()
    candidate_sha = hashlib.sha256(candidate_bytes).hexdigest()
    candidate = json.loads(candidate_bytes)
    technical = json.loads(technical_path.read_text(encoding="utf-8"))
    descriptor = json.loads(descriptor_path.read_text(encoding="utf-8"))
    if descriptor.get("session_id") != session_id or candidate.get("source_sha256") != descriptor.get("source_sha256"):
        raise ValueError("candidate/source identity mismatch")
    review = json.loads(review_path.read_text(encoding="utf-8"))
    validate_semantic_review(review, session_id=session_id, candidate_sha256=candidate_sha)
    validate_v2_deep(candidate, descriptor)
    validate_source_roles(candidate, descriptor)
    reject_applied_duplicates(candidate, data_dir)  # journal may have changed after worker validation
    validate_telemetry(technical, descriptor)
    unfinished = [check["id"] for check in candidate["checks"] if check["status"] == "not_checked"]
    if unfinished:
        raise ValueError("unfinished Deep checks: " + ", ".join(unfinished))
    build_index(descriptor, data_dir / "v2" / "review-index")  # recheck frozen prefix before publication

    destination = data_dir / "v2" / "deep" / f"{session_id}.json"
    technical_destination = data_dir / "telemetry" / f"{session_id}.json"
    registry_path = data_dir / "v2" / "proposals.json"
    previous = [(label, path, path.read_bytes() if path.is_file() else None) for label, path in
                (("deep", destination), ("technical", technical_destination),
                 ("source", published_descriptor_path), ("registry", registry_path))]
    if (previous[0][2] == candidate_bytes and previous[1][2] == technical_path.read_bytes()
            and previous[2][2] == descriptor_path.read_bytes()):
        return destination
    history = data_dir / "v2" / "history" / session_id
    for label, published_path, old_bytes in previous:
        if old_bytes is not None:
            atomic_private_bytes(history / f"{label}-{hashlib.sha256(old_bytes).hexdigest()}.json", old_bytes)
    try:
        atomic_json(published_descriptor_path, descriptor)
        atomic_json(destination, candidate)
        atomic_json(technical_destination, technical)
        registry_refresh(data_dir)
    except Exception:
        for _, published_path, old_bytes in previous:
            if old_bytes is None:
                published_path.unlink(missing_ok=True)
            else:
                atomic_private_bytes(published_path, old_bytes)
        raise
    request_path = data_dir / "v2" / "requests" / f"{session_id}.json"
    if request_path.is_file():
        request = json.loads(request_path.read_text(encoding="utf-8"))
        request.update(status="completed", published=True, validation="structural_and_semantic_review", proposal_refresh="updated", reviewed_at=review["reviewed_at"], reviewer=review["reviewer"], published_at=now(), result_path=str(destination), technical_result_path=str(technical_destination))
        atomic_json(request_path, request)
    return destination


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("data_dir", type=Path)
    parser.add_argument("session_id")
    parser.add_argument("review_file", type=Path)
    args = parser.parse_args()
    published = publish(args.data_dir.resolve(), args.session_id, args.review_file.resolve())
    print(published)


if __name__ == "__main__":
    main()
