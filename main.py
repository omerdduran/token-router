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
import agent
from agent import solve
from llm import describe_tiers, usage

INPUT_PATH = os.environ.get("INPUT_PATH", "/input/tasks.json")
OUTPUT_PATH = os.environ.get("OUTPUT_PATH", "/output/results.json")
MAX_WORKERS = int(os.environ.get("MAX_WORKERS", "8"))
# Stop with headroom before the harness's 10-minute kill so results.json is
# always written, even if a few tasks never come back.
DEADLINE_S = float(os.environ.get("DEADLINE_S", "480"))
# Hard wall-clock ceiling for the WHOLE run (local inference + Fireworks pool),
# measured from process start, kept well under the 600s harness kill. The local
# routing loop and the Fireworks pool are serial, so each must respect this or
# their budgets could sum past 600s and TIMEOUT.
GLOBAL_DEADLINE_S = float(os.environ.get("GLOBAL_DEADLINE_S", "540"))
# Local generation stops this many seconds before GLOBAL_DEADLINE_S, so a task
# that sheds late (blank/truncated local output) still gets one parallel round
# of Fireworks calls before the drain deadline.
REMOTE_RESERVE_S = float(os.environ.get("REMOTE_RESERVE_S", "45"))
_START = time.monotonic()


def _global_deadline() -> float:
    return _START + GLOBAL_DEADLINE_S


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


def _answer_remote(task: dict, index: int, spec) -> dict:
    """Pool worker for a task already routed to Fireworks (spec = system,
    max_tokens, tier), or with spec None for a full solve()."""
    task_id = task.get("task_id", f"idx_{index}")
    try:
        if spec is None:
            answer = solve(task.get("prompt", ""))
        else:
            answer = agent.solve_remote(task.get("prompt", ""), *spec)
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


def _store(index: int, task: dict, answer: str) -> None:
    with _results_lock:
        _results[index] = {"task_id": task.get("task_id", f"idx_{index}"), "answer": answer}


def _run_pool(work: list[tuple]) -> None:
    """Run (index, task, spec) work items via the pool, flushing after each
    completion and stopping at the deadline. spec None → full solve()."""
    if not work:
        return
    # Whichever comes first: the pool's own budget, or the global ceiling (so
    # time already spent on local inference is subtracted here).
    deadline = min(time.monotonic() + DEADLINE_S, _global_deadline())
    timeout = max(1.0, deadline - time.monotonic())
    pool = ThreadPoolExecutor(max_workers=min(MAX_WORKERS, len(work)))
    fut_to_idx = {pool.submit(_answer_remote, task, i, spec): i for i, task, spec in work}
    try:
        for fut in as_completed(fut_to_idx, timeout=timeout):
            idx = fut_to_idx[fut]
            try:
                with _results_lock:
                    _results[idx] = fut.result()
            except Exception:
                pass
            _flush()
            if time.monotonic() > deadline:
                break
    except Exception:
        pass
    pool.shutdown(wait=False, cancel_futures=True)


def run(tasks: list[dict]) -> list[dict]:
    global _results
    # Skeleton first: every task_id present with a blank answer, on disk before
    # any model call, so the output contract holds even if we die early.
    _results = [{"task_id": t.get("task_id", f"idx_{i}"), "answer": ""}
                for i, t in enumerate(tasks)]
    _flush()
    if not tasks:
        return _results

    if not agent._BATCH:
        _run_pool([(i, t, None) for i, t in enumerate(tasks)])
        _flush()
        return _results

    # Batch mode. The bottleneck this removes: llama.cpp is single-locked, so
    # local inference is SERIAL, while the Fireworks pool is PARALLEL. Running
    # local first and the pool second sums their wall-clock; doing too much
    # locally then overran the deadline and left tasks blank. Instead we run the
    # two streams CONCURRENTLY so they overlap (wall-clock = max, not sum):
    #   Phase 1 — classify + free solvers only (no local inference, no API yet),
    #             splitting the rest into local-eligible and remote.
    #   Phase 2 — submit remote work to the pool, then walk the local jobs on
    #             this thread WHILE the pool runs. A local job that would miss the
    #             deadline, or returns blank, sheds to the pool — a few tokens,
    #             but never a blank answer.
    local_jobs: list[tuple] = []   # (i, task, category, system, prompt, max_tokens, tier)
    remote_jobs: list[tuple] = []  # (i, task, spec)
    for i, t in enumerate(tasks):
        prompt = t.get("prompt", "")
        try:
            r = agent.route(prompt, allow_local=False)  # solver may answer; local skipped
        except Exception:
            remote_jobs.append((i, t, None))
            continue
        if r[0] == "done":            # a deterministic solver answered
            _store(i, t, r[1])
            continue
        _, category, system, max_tokens, tier = r
        if local.available_for(category.value):
            local_jobs.append((i, t, category.value, system, prompt, max_tokens, tier))
        else:
            remote_jobs.append((i, t, (system, max_tokens, tier)))
    # Cheapest-first: short-output categories cost the fewest local seconds per
    # remote token saved, so under time pressure they all get done and only the
    # slow ones (math/logic — which the solvers may have caught anyway) shed.
    # Same-category grouping also lets llama.cpp's prefix cache skip re-eval of
    # the shared system prompt.
    local_jobs.sort(key=lambda j: local.sort_key(j[2], len(j[4])))
    _flush()

    pool = ThreadPoolExecutor(max_workers=MAX_WORKERS)
    futures: dict = {}
    fut_lock = threading.Lock()

    def _submit(idx: int, task: dict, spec) -> None:
        fut = pool.submit(_answer_remote, task, idx, spec)
        with fut_lock:
            futures[fut] = idx

    for i, t, spec in remote_jobs:
        _submit(i, t, spec)

    # Local worker on this thread, overlapping the pool. Generation streams
    # token by token (local.complete deadline=), so it is interruptible: a task
    # still running at hard_stop returns truncated instead of blocking past the
    # harness kill (the v26 TIMEOUT). That kills the need for the old
    # conservative 0.65 cutoff — local work runs to within REMOTE_RESERVE_S of
    # the global ceiling, and the reserve gives late sheds one parallel round
    # of Fireworks calls. Before each task, its measured-speed time estimate
    # decides up front whether it can still fit; a task that can't sheds
    # immediately so the pool works on it WHILE local inference continues.
    hard_stop = _global_deadline() - REMOTE_RESERVE_S
    n_local = n_shed_time = n_shed_blank = 0
    for (i, t, cat, system, prompt, max_tokens, tier) in local_jobs:
        now = time.monotonic()
        if now + local.est_seconds(cat, len(prompt)) > hard_stop:
            _submit(i, t, (system, max_tokens, tier))    # won't fit → Fireworks
            n_shed_time += 1
            continue
        try:
            ans = local.complete(system, prompt, max_tokens, deadline=hard_stop)
        except Exception:
            ans = ""
        if ans:   # full or truncated — either way zero tokens, and never blank
            _store(i, t, ans)
            _flush()
            n_local += 1
        else:
            _submit(i, t, (system, max_tokens, tier))    # blank → Fireworks
            n_shed_blank += 1
    if local_jobs:
        print(f"local: {n_local}/{len(local_jobs)} answered locally, "
              f"shed {n_shed_time} (time) + {n_shed_blank} (blank), "
              f"tok/s={local.tok_s():.1f}", file=sys.stderr)

    # Drain the pool, bounded by the global ceiling.
    deadline = _global_deadline()
    with fut_lock:
        pending = dict(futures)
    try:
        for fut in as_completed(pending, timeout=max(1.0, deadline - time.monotonic())):
            with _results_lock:
                try:
                    _results[pending[fut]] = fut.result()
                except Exception:
                    pass
            _flush()
            if time.monotonic() > deadline:
                break
    except Exception:
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
