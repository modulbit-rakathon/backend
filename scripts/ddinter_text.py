#!/usr/bin/env python3
"""Build internal/interactions/ddinter_text.csv.gz: what an interaction does.

The DDInter download (see ddinter.py) has only drug pairs and a severity
level. The description ("Interaction") and the advice ("Management") are only
on the website, one page per pair. This script fetches them for major and
moderate pairs between the substances in substances.txt (the catalogue and
the regimen table, from cmd/ddinter-substances), a few thousand pages.

Output rows: drug_a,drug_b,interaction_id,description,management with
drug_a < drug_b, sorted. The texts are DDInter's English originals.
Licence: CC BY-NC-SA 4.0, see LICENSE-DDInter.md.

Usage:
  (download the CSVs into /tmp/ddinter as in ddinter.py)
  go run ./cmd/ddinter-substances > /tmp/ddinter/substances.txt
  scripts/ddinter_text.py /tmp/ddinter /tmp/ddinter/substances.txt internal/interactions
"""
import concurrent.futures
import csv
import gzip
import html
import io
import json
import os
import re
import sys
import time
import urllib.parse
import urllib.request

SITE = "https://ddinter.scbdd.com"
LEVELS = {"Major", "Moderate"}
WORKERS = 4  # keep the load on DDInter low


def get(url, data=None):
    body = urllib.parse.urlencode(data).encode() if data else None
    headers = {"User-Agent": "PillGaze build script (hackathon, non-commercial)"}
    if data:
        headers["Referer"] = SITE + "/ddinter/drug/"
    for attempt in range(4):
        try:
            req = urllib.request.Request(url, data=body, headers=headers)
            with urllib.request.urlopen(req, timeout=30) as res:
                return res.read().decode("utf-8")
        except Exception as err:  # network hiccups: retry with backoff
            if attempt == 3:
                raise RuntimeError(f"{url}: {err}") from err
            time.sleep(2 ** attempt)


def read_download(src, wanted):
    """DDInter IDs of the wanted names and their major/moderate pairs."""
    ids, pairs = {}, set()
    for name in sorted(os.listdir(src)):
        if not name.endswith(".csv"):
            continue
        with open(os.path.join(src, name), encoding="utf-8") as f:
            for row in csv.DictReader(f):
                a, b = row["Drug_A"].strip(), row["Drug_B"].strip()
                ids.setdefault(a, row["DDInterID_A"].strip())
                ids.setdefault(b, row["DDInterID_B"].strip())
                if a in wanted and b in wanted and a != b and row["Level"].strip() in LEVELS:
                    pairs.add(tuple(sorted((a, b))))
    return ids, pairs


def interaction_ids(drug_id):
    """Partner name -> interaction page id, from the table on the drug page."""
    data = json.loads(get(f"{SITE}/ddinter/interact-with/{drug_id}/", {"draw": 1, "start": 0, "length": 5000}))
    return {row["drug_name"]: row["interaction_id"] for row in data["data"]}


def cell(page, key):
    """Text of the table cell next to <td class="key">key</td>, without the effect badges."""
    m = re.search(r'<td class="key">' + key + r'</td>\s*<td[^>]*>(.*?)</td>', page, re.S)
    if not m:
        return ""
    text = m.group(1).split('<div id="effect-block"')[0]
    text = re.sub(r"<[^>]+>", " ", text)
    return re.sub(r"\s+", " ", html.unescape(text)).strip()


def fetch_text(interaction_id):
    page = get(f"{SITE}/ddinter/interact/{interaction_id}/")
    return cell(page, "Interaction"), cell(page, "Management")


def main(src, substances, dst):
    with open(substances, encoding="utf-8") as f:
        wanted = {line.strip() for line in f if line.strip()}
    ids, pairs = read_download(src, wanted)
    missing = sorted(n for n in wanted if n not in ids)
    if missing:
        sys.exit(f"not in the download: {missing}")
    print(f"{len(wanted)} substances, {len(pairs)} major/moderate pairs", file=sys.stderr)

    drugs = sorted({n for p in pairs for n in p})
    page_ids = {}
    with concurrent.futures.ThreadPoolExecutor(WORKERS) as pool:
        for name, partners in zip(drugs, pool.map(lambda n: interaction_ids(ids[n]), drugs)):
            for partner, pid in partners.items():
                if partner in wanted:
                    page_ids[tuple(sorted((name, partner)))] = pid
    todo = sorted(p for p in pairs if p in page_ids)
    print(f"{len(todo)} pages to fetch, {len(pairs) - len(todo)} pairs without a page", file=sys.stderr)

    rows = []
    with concurrent.futures.ThreadPoolExecutor(WORKERS) as pool:
        futures = {pool.submit(fetch_text, page_ids[p]): p for p in todo}
        for i, fut in enumerate(concurrent.futures.as_completed(futures), 1):
            a, b = futures[fut]
            description, management = fut.result()
            if description or management:
                rows.append((a, b, page_ids[(a, b)], description, management))
            if i % 200 == 0:
                print(f"{i}/{len(todo)}", file=sys.stderr)
    rows.sort()

    buf = io.StringIO()
    csv.writer(buf, lineterminator="\n").writerows(rows)
    out = os.path.join(dst, "ddinter_text.csv.gz")
    with gzip.GzipFile(out, "wb", mtime=0) as f:
        f.write(buf.getvalue().encode("utf-8"))
    print(f"{len(rows)} pairs with text -> {out} ({os.path.getsize(out)} bytes)", file=sys.stderr)


if __name__ == "__main__":
    if len(sys.argv) != 4:
        sys.exit(__doc__)
    main(*sys.argv[1:])
