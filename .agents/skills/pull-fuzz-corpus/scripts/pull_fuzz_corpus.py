#!/usr/bin/env python3
"""Pull failing fuzz test corpus files from GitHub Actions CI.

Searches recent CI runs for fuzz test failures, downloads the corpus
artifacts, and copies them into the correct testdata/fuzz/ directories.

A high watermark file (.fuzz-corpus-watermark.json) tracks which runs
have already been processed so the same failures aren't pulled twice.
"""

import argparse
import json
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path


WATERMARK_FILE = ".fuzz-corpus-watermark.json"

# Workflows that run fuzz tests
FUZZ_WORKFLOWS = ["fuzz-full.yml", "ci.yml"]

# All possible fuzz corpus artifact names to try when scanning a run
ALL_ARTIFACT_NAMES = [
    "fuzz-full-shard-1-corpus",
    "fuzz-full-shard-2-corpus",
    "fuzz-full-shard-3-corpus",
    "fuzz-full-shard-4-corpus",
    "fuzz-pr-deep-shard-1-corpus",
    "fuzz-pr-deep-shard-2-corpus",
    "fuzz-pr-deep-shard-3-corpus",
    "fuzz-pr-deep-shard-4-corpus",
    "fuzz-pr-jq-corpus",
    "fuzz-pr-yq-corpus",
    "fuzz-pr-sqlite3-corpus",
]


@dataclass
class FuzzFailure:
    run_id: int
    job_name: str
    job_id: int
    test_name: str
    corpus_file: str
    failure_message: str


@dataclass
class PullResult:
    failures_found: list[FuzzFailure] = field(default_factory=list)
    files_copied: list[str] = field(default_factory=list)
    runs_checked: list[int] = field(default_factory=list)
    errors: list[str] = field(default_factory=list)


def get_repo_root() -> Path:
    result = subprocess.run(
        ["git", "rev-parse", "--show-toplevel"],
        capture_output=True, text=True, check=True,
    )
    return Path(result.stdout.strip())


def load_watermark(repo_root: Path) -> dict:
    path = repo_root / WATERMARK_FILE
    if path.exists():
        return json.loads(path.read_text())
    return {"last_run_ids": {}, "last_checked": None}


def save_watermark(repo_root: Path, watermark: dict):
    path = repo_root / WATERMARK_FILE
    watermark["last_checked"] = datetime.now(timezone.utc).isoformat()
    path.write_text(json.dumps(watermark, indent=2) + "\n")


def gh_json(args: list[str]) -> list | dict:
    result = subprocess.run(
        ["gh"] + args,
        capture_output=True, text=True, check=True,
    )
    return json.loads(result.stdout)


def list_failed_runs(workflow: str, limit: int = 20) -> list[dict]:
    runs = gh_json([
        "run", "list",
        "--workflow", workflow,
        "--limit", str(limit),
        "--json", "databaseId,conclusion,createdAt,headBranch,event",
    ])
    return [r for r in runs if r["conclusion"] == "failure"]


def get_failed_fuzz_jobs(run_id: int, job_id: int | None = None) -> list[dict]:
    jobs = gh_json([
        "run", "view", str(run_id),
        "--json", "jobs",
    ])
    failed = []
    for job in jobs.get("jobs", []):
        name = job.get("name", "")
        conclusion = job.get("conclusion", "")
        jid = job.get("databaseId")

        # If a specific job ID was given, only look at that job
        if job_id and jid != job_id:
            continue

        is_fuzz = "fuzz" in name.lower()
        # Match failure, or non-success when targeting a specific job
        is_failed = conclusion == "failure" or (job_id and conclusion != "success")

        if is_fuzz and is_failed:
            failed.append({
                "name": name,
                "id": jid,
            })
    return failed


def parse_fuzz_failures_from_log(run_id: int, job_id: int, job_name: str) -> list[FuzzFailure]:
    result = subprocess.run(
        ["gh", "run", "view", str(run_id), "--log", "--job", str(job_id)],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        return []

    lines = result.stdout.splitlines()
    failures = []
    for i, line in enumerate(lines):
        if "Failing input written to" not in line:
            continue

        # Extract corpus path: "Failing input written to testdata/fuzz/FuzzFoo/abc123"
        parts = line.split("Failing input written to ")
        if len(parts) < 2:
            continue
        corpus_path = parts[1].strip()

        # Extract test name from corpus path
        # e.g. testdata/fuzz/FuzzGeneratedPrograms/b6805dcc35ae491f
        path_parts = corpus_path.split("/")
        test_name = ""
        for j, p in enumerate(path_parts):
            if p.startswith("Fuzz"):
                test_name = p
                break

        # Gather failure message from surrounding lines
        failure_msg = ""
        for j in range(max(0, i - 5), min(len(lines), i + 3)):
            stripped = lines[j].split("\t")[-1].strip()  # strip GH Actions prefix
            # Remove timestamp prefix
            if "Z " in stripped:
                stripped = stripped.split("Z ", 1)[1]
            failure_msg += stripped + "\n"

        failures.append(FuzzFailure(
            run_id=run_id,
            job_name=job_name,
            job_id=job_id,
            test_name=test_name,
            corpus_file=corpus_path,
            failure_message=failure_msg.strip(),
        ))

    return failures


def guess_artifact_name(job_name: str) -> list[str]:
    """Guess the artifact name(s) from the job name."""
    name_lower = job_name.lower()
    candidates = []

    # "Fuzz Full (shard-3)" -> "fuzz-full-shard-3-corpus"
    if "fuzz full" in name_lower:
        for i in range(1, 5):
            if f"shard-{i}" in name_lower:
                candidates.append(f"fuzz-full-shard-{i}-corpus")

    # "Fuzz PR Deep (shard-3)" -> "fuzz-pr-deep-shard-3-corpus"
    if "fuzz pr deep" in name_lower:
        for i in range(1, 5):
            if f"shard-{i}" in name_lower:
                candidates.append(f"fuzz-pr-deep-shard-{i}-corpus")
        if "jq" in name_lower:
            candidates.append("fuzz-pr-jq-corpus")
        if "yq" in name_lower:
            candidates.append("fuzz-pr-yq-corpus")
        if "sqlite3" in name_lower:
            candidates.append("fuzz-pr-sqlite3-corpus")

    return candidates


def list_fuzz_artifacts(run_id: int) -> list[str]:
    """List available fuzz corpus artifacts for a run."""
    try:
        result = subprocess.run(
            ["gh", "api", f"repos/{{owner}}/{{repo}}/actions/runs/{run_id}/artifacts",
             "--jq", ".artifacts[].name"],
            capture_output=True, text=True, check=True,
        )
        names = [n.strip() for n in result.stdout.splitlines() if n.strip()]
        return [n for n in names if "fuzz" in n.lower() and "corpus" in n.lower()]
    except subprocess.CalledProcessError:
        return []


def download_and_copy_corpus(
    run_id: int,
    artifact_name: str,
    repo_root: Path,
) -> list[str]:
    """Download a fuzz corpus artifact and copy files into the repo."""
    copied = []

    with tempfile.TemporaryDirectory() as tmpdir:
        result = subprocess.run(
            ["gh", "run", "download", str(run_id),
             "--name", artifact_name, "--dir", tmpdir],
            capture_output=True, text=True,
        )
        if result.returncode != 0:
            if "no valid artifacts found" in result.stderr.lower():
                return []
            raise RuntimeError(
                f"Failed to download artifact {artifact_name}: {result.stderr}"
            )

        # The artifact has files/ directory mirroring the repo structure
        files_dir = Path(tmpdir) / "files"
        if not files_dir.exists():
            return []

        for corpus_file in files_dir.rglob("*"):
            if not corpus_file.is_file():
                continue

            # Get the relative path within files/
            rel = corpus_file.relative_to(files_dir)
            dest = repo_root / rel
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(corpus_file, dest)
            copied.append(str(rel))

    return copied


def pull_fuzz_corpus(
    run_id: int | None = None,
    job_id: int | None = None,
    all_new: bool = False,
    dry_run: bool = False,
) -> PullResult:
    repo_root = get_repo_root()
    watermark = load_watermark(repo_root)
    result = PullResult()

    if run_id:
        # Process a specific run
        run_ids_to_check = {run_id: "manual"}
    else:
        # Find failed runs across all fuzz workflows
        run_ids_to_check = {}
        for workflow in FUZZ_WORKFLOWS:
            try:
                failed = list_failed_runs(workflow)
            except subprocess.CalledProcessError:
                continue
            for run in failed:
                rid = run["databaseId"]
                if not all_new and str(rid) in watermark.get("last_run_ids", {}):
                    continue
                run_ids_to_check[rid] = workflow

    if not run_ids_to_check:
        print("No new failed fuzz runs found.")
        return result

    for rid, source in run_ids_to_check.items():
        result.runs_checked.append(rid)
        run_had_errors = False
        run_files_copied = []
        print(f"\nChecking run {rid} ({source})...")

        try:
            failed_jobs = get_failed_fuzz_jobs(rid, job_id=job_id)
        except subprocess.CalledProcessError as e:
            result.errors.append(f"Failed to get jobs for run {rid}: {e}")
            continue

        if not failed_jobs:
            # Jobs may have been re-triggered, but artifacts from the
            # original failed run can still be available. Try to find
            # and download any fuzz corpus artifacts directly.
            print(f"  No failed fuzz jobs found, checking for artifacts...")
            artifacts = list_fuzz_artifacts(rid)
            if artifacts and not dry_run:
                for aname in artifacts:
                    print(f"    Downloading artifact: {aname}")
                    try:
                        copied = download_and_copy_corpus(rid, aname, repo_root)
                        result.files_copied.extend(copied)
                        run_files_copied.extend(copied)
                        for c in copied:
                            print(f"      Copied: {c}")
                    except RuntimeError as e:
                        result.errors.append(str(e))
                        run_had_errors = True
                        print(f"      Error: {e}")
            elif artifacts and dry_run:
                print(f"    Found artifacts: {', '.join(artifacts)}")
                print(f"    (dry run, skipping download)")
            else:
                print(f"  No artifacts found either")
            if not run_had_errors:
                watermark.setdefault("last_run_ids", {})[str(rid)] = {
                    "checked": datetime.now(timezone.utc).isoformat(),
                    "status": "artifacts_only" if artifacts else "no_fuzz_failures",
                }
            if not artifacts:
                continue

        for job in failed_jobs:
            print(f"  Checking job: {job['name']} (id={job['id']})")

            failures = parse_fuzz_failures_from_log(rid, job["id"], job["name"])
            result.failures_found.extend(failures)

            if not failures:
                print(f"    No corpus files found (may be a timeout without a reproducer)")
                continue

            for f in failures:
                print(f"    Found: {f.test_name} -> {f.corpus_file}")
                print(f"    {f.failure_message.splitlines()[0] if f.failure_message else ''}")

            if dry_run:
                print("    (dry run, skipping download)")
                continue

            # Try to download artifacts
            artifact_names = guess_artifact_name(job["name"])
            for aname in artifact_names:
                print(f"    Downloading artifact: {aname}")
                try:
                    copied = download_and_copy_corpus(rid, aname, repo_root)
                    result.files_copied.extend(copied)
                    run_files_copied.extend(copied)
                    for c in copied:
                        print(f"      Copied: {c}")
                except RuntimeError as e:
                    result.errors.append(str(e))
                    run_had_errors = True
                    print(f"      Error: {e}")

        # Only mark as processed if there were no download errors for
        # this run. Transient failures (auth issues, network errors)
        # should allow retries on the next invocation without needing
        # --all or --reset-watermark.
        if not run_had_errors:
            watermark.setdefault("last_run_ids", {})[str(rid)] = {
                "checked": datetime.now(timezone.utc).isoformat(),
                "status": "processed",
                "files_copied": run_files_copied,
            }

    if not dry_run:
        save_watermark(repo_root, watermark)

    return result


def main():
    parser = argparse.ArgumentParser(
        description="Pull failing fuzz corpus files from CI"
    )
    parser.add_argument(
        "--run-id", type=int,
        help="Process a specific GitHub Actions run ID",
    )
    parser.add_argument(
        "--job-id", type=int,
        help="Target a specific job ID within the run",
    )
    parser.add_argument(
        "--all", action="store_true", dest="all_new",
        help="Re-check all failed runs, ignoring the watermark",
    )
    parser.add_argument(
        "--dry-run", action="store_true",
        help="Show what would be downloaded without actually doing it",
    )
    parser.add_argument(
        "--reset-watermark", action="store_true",
        help="Reset the high watermark file",
    )

    args = parser.parse_args()

    if args.reset_watermark:
        repo_root = get_repo_root()
        wm_path = repo_root / WATERMARK_FILE
        if wm_path.exists():
            wm_path.unlink()
            print(f"Removed {WATERMARK_FILE}")
        else:
            print(f"No watermark file found")
        return

    result = pull_fuzz_corpus(
        run_id=args.run_id,
        job_id=args.job_id,
        all_new=args.all_new,
        dry_run=args.dry_run,
    )

    # Summary
    print("\n" + "=" * 60)
    print(f"Runs checked:    {len(result.runs_checked)}")
    print(f"Failures found:  {len(result.failures_found)}")
    print(f"Files copied:    {len(result.files_copied)}")
    if result.errors:
        print(f"Errors:          {len(result.errors)}")
        for e in result.errors:
            print(f"  - {e}")

    if result.files_copied:
        print("\nCopied files:")
        for f in result.files_copied:
            print(f"  {f}")
        print("\nRun the failing tests to verify:")
        for f in result.files_copied:
            # f is like "internal/runtime/testdata/fuzz/FuzzGeneratedPrograms/hash"
            if "/testdata/fuzz/" in f:
                pkg = f.split("/testdata/fuzz/")[0]
                fuzz_suffix = f.split("/testdata/fuzz/")[1]
                test_name = fuzz_suffix.split("/")[0]
                corpus_name = fuzz_suffix.split("/")[1]
                print(f"  go test ./{pkg} -run='{test_name}/{corpus_name}'")

    if not result.files_copied and not args.dry_run:
        if result.failures_found:
            print("\nFailures were found but no artifacts could be downloaded.")
            print("The artifacts may have expired or the failure was a timeout")
            print("without a reproducible corpus entry.")
        else:
            print("\nNo new fuzz failures to process.")

    sys.exit(1 if result.errors else 0)


if __name__ == "__main__":
    main()
