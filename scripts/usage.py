#!/usr/bin/env python3
"""Sum model tokens from the App Service container logs and saved eval runs.

Every request line the server logs carries the model and the tokens of all
its model calls (no drug names, no bodies). Container logging to the file
system is on since 2026-10-16 (3 days, 100 MB); requests before that are
mostly lost.

Usage:
  mkdir -p /tmp/pillgaze-logs && cd /tmp/pillgaze-logs
  az webapp log download -n pillgaze-team92 -g rg-team92 --log-file logs.zip
  unzip -o -q logs.zip -d logs
  scripts/usage.py /tmp/pillgaze-logs/logs [data/eval]
"""
import collections
import glob
import json
import os
import re
import sys

LINE = re.compile(r'(\{"time".*\})')


def production(logdir, tot):
    seen = set()
    for path in glob.glob(os.path.join(logdir, "**", "*.log"), recursive=True):
        with open(path, errors="replace") as f:
            for line in f:
                m = LINE.search(line)
                if not m:
                    continue
                try:
                    d = json.loads(m.group(1))
                except ValueError:
                    continue
                if d.get("msg") != "request" or "prompt_tokens" not in d or d["request_id"] in seen:
                    continue
                seen.add(d["request_id"])
                add(tot, "production", d.get("model", "?"), d["prompt_tokens"], d["completion_tokens"])


def evals(evaldir, tot):
    for path in glob.glob(os.path.join(evaldir, "2*.json")):
        with open(path) as f:
            for r in json.load(f):
                if "prompt_tokens" in r:
                    add(tot, "eval", r.get("model", "?"), r["prompt_tokens"], r["completion_tokens"])


def add(tot, source, model, prompt, completion):
    t = tot[(source, model)]
    t[0] += 1
    t[1] += prompt
    t[2] += completion


def main(logdir, evaldir=None):
    tot = collections.defaultdict(lambda: [0, 0, 0])
    production(logdir, tot)
    if evaldir:
        evals(evaldir, tot)
    print(f'{"source":11} {"model":20} {"requests":>8} {"prompt":>10} {"completion":>11}')
    for (source, model), (n, p, c) in sorted(tot.items()):
        print(f"{source:11} {model:20} {n:8} {p:10} {c:11}")


if __name__ == "__main__":
    if len(sys.argv) not in (2, 3):
        sys.exit(__doc__)
    main(*sys.argv[1:])
