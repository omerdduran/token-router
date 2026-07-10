"""Track 1 entrypoint: read /input/tasks.json, answer each task, write
/output/results.json, exit 0.

The judging harness mounts /input and /output at the filesystem root and
injects FIREWORKS_API_KEY, FIREWORKS_BASE_URL, ALLOWED_MODELS. Paths are
overridable via INPUT_PATH / OUTPUT_PATH for local development.
"""

from __future__ import annotations

import json
import os
import signal
import sys
import threading
import time
import traceback
from concurrent.futures import ThreadPoolExecutor, as_completed

import local
from agent import solve
from llm import describe_tiers, usage

INPUT_PATH = os.environ.get("INPUT_PATH", "/input/tasks.json")
OUTPUT_PATH = os.environ.get("OUTPUT_PATH", "/output/results.json")
MAX_WORKERS = int(os.environ.get("MAX_WORKERS", "8"))
# Stop with headroom before the harness's 10-minute kill so results.json is
# always written, even if a few tasks never come back.
DEADLINE_S = float(os.environ.get("DEADLINE_S", "480"))


def load_tasks(path: str) -> list[dict]:
    with open(path, encoding="utf-8") as fh:
        tasks = json.load(fh)
    if not isinstance(tasks, list):
        raise ValueError(f"expected a JSON list, got {type(tasks).__name__}")
    return tasks


def write_results(path: str, results: list[dict]) -> None:
    parent = os.path.dirname(path)
    if parent:
        os.makedirs(parent, exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(results, fh, ensure_ascii=False, indent=2)


def _answer_one(task: dict, index: int) -> dict:
    # Echo task_id exactly as given (numbers stay numbers); fabricate a
    # stable one only when the input omits it.
    task_id = task.get("task_id", f"idx_{index}")
    try:
        answer = solve(task.get("prompt", ""))
    except Exception:
        traceback.print_exc()
        answer = ""
    return {"task_id": task_id, "answer": answer}


# Live results, persisted incrementally so a kill (OOM or the harness's
# timeout) still leaves a valid, scoreable results.json instead of nothing —
# the difference between a partial score and an INFRA_ERROR. The local model
# runs on a 4GB/2vCPU box where either failure is possible.
_results: list[dict] = []
_results_lock = threading.Lock()
_output_path = OUTPUT_PATH


def _flush() -> None:
    with _results_lock:
        snapshot = [dict(r) for r in _results]
    try:
        write_results(_output_path, snapshot)
    except Exception as exc:
        print(f"WARN: flush failed: {exc}", file=sys.stderr)


def _on_signal(signum, _frame):
    print(f"signal {signum}: flushing partial results", file=sys.stderr)
    _flush()
    os._exit(0)


def run(tasks: list[dict]) -> list[dict]:
    global _results
    # Skeleton first: every task_id present with a blank answer, on disk before
    # any model call, so the output contract holds even if we die early.
    _results = [{"task_id": t.get("task_id", f"idx_{i}"), "answer": ""}
                for i, t in enumerate(tasks)]
    _flush()
    if not tasks:
        return _results

    deadline = time.monotonic() + DEADLINE_S
    pool = ThreadPoolExecutor(max_workers=min(MAX_WORKERS, len(tasks)))
    fut_to_idx = {pool.submit(_answer_one, t, i): i for i, t in enumerate(tasks)}

    try:
        for fut in as_completed(fut_to_idx, timeout=max(1.0, DEADLINE_S)):
            idx = fut_to_idx[fut]
            try:
                res = fut.result()
                with _results_lock:
                    _results[idx] = res
            except Exception:
                pass
            _flush()  # persist after every completion
            if time.monotonic() > deadline:
                break
    except Exception:  # as_completed timeout or similar: keep the partial file
        pass
    pool.shutdown(wait=False, cancel_futures=True)
    _flush()
    return _results


def main() -> int:
    global _output_path
    _output_path = OUTPUT_PATH
    # Flush partial results if the harness kills us (SIGTERM at the time limit).
    for sig in (signal.SIGTERM, signal.SIGINT):
        try:
            signal.signal(sig, _on_signal)
        except (ValueError, OSError):
            pass

    missing = [k for k in ("FIREWORKS_API_KEY", "FIREWORKS_BASE_URL", "ALLOWED_MODELS")
               if not os.environ.get(k)]
    if missing:
        print(f"FATAL: missing environment variables: {', '.join(missing)}",
              file=sys.stderr)
        return 1

    try:
        tasks = load_tasks(INPUT_PATH)
    except Exception as exc:
        print(f"FATAL: cannot read tasks from {INPUT_PATH}: {exc}", file=sys.stderr)
        return 1

    print(f"Loaded {len(tasks)} task(s) from {INPUT_PATH}", file=sys.stderr)
    try:
        print(f"Model tiers: {describe_tiers()}", file=sys.stderr)
    except Exception as exc:
        print(f"WARN: could not resolve model tiers: {exc}", file=sys.stderr)

    # Skeleton before loading the local model: an OOM during model load is a
    # SIGKILL we can't catch, so a valid (blank) results.json must already be
    # on disk. run() then updates it in place.
    global _results
    with _results_lock:
        _results = [{"task_id": t.get("task_id", f"idx_{i}"), "answer": ""}
                    for i, t in enumerate(tasks)]
    _flush()

    # Bring up the bundled local model before answering (best-effort; degrades
    # to Fireworks-only on any failure).
    try:
        local.start()
    except Exception as exc:
        print(f"WARN: local model unavailable: {exc}", file=sys.stderr)

    results = run(tasks)

    try:
        write_results(OUTPUT_PATH, results)
    except Exception as exc:
        print(f"FATAL: cannot write results to {OUTPUT_PATH}: {exc}", file=sys.stderr)
        return 1

    u = usage()
    print(f"Wrote {len(results)} result(s) to {OUTPUT_PATH} | tokens: total={u['total']} "
          f"(prompt={u['prompt']} completion={u['completion']}) over {u['calls']} call(s)",
          file=sys.stderr)
    return 0


if __name__ == "__main__":
    _rc = main()
    sys.stdout.flush()
    sys.stderr.flush()
    # Hard exit: a blocking local-inference thread is non-interruptible and
    # would otherwise keep the process alive past the deadline. results.json is
    # already flushed, so this is safe.
    os._exit(_rc)
