#!/usr/bin/env python3
"""Build internal/interactions/ddinter.csv.gz from the DDInter download.

DDInter (https://ddinter.scbdd.com, Xiong et al., NAR 2022) publishes one
CSV per ATC group: DDInterID_A,Drug_A,DDInterID_B,Drug_B,Level. The groups
overlap, so pairs are deduplicated. Output rows: drug_a,drug_b,level with
drug_a < drug_b, sorted. Licence: CC BY-NC-SA 4.0, see the LICENSE file
written next to the output.

Usage:
  mkdir -p /tmp/ddinter && cd /tmp/ddinter
  for c in A B C D G H J L M N P R S V; do
    curl -sO https://ddinter.scbdd.com/static/media/download/ddinter_downloads_code_$c.csv
  done
  scripts/ddinter.py /tmp/ddinter internal/interactions
"""
import csv
import datetime
import gzip
import io
import os
import sys

LEVELS = {"Major": "major", "Moderate": "moderate", "Minor": "minor", "Unknown": "unknown"}


def main(src, dst):
    pairs = {}
    for name in sorted(os.listdir(src)):
        if not name.endswith(".csv"):
            continue
        with open(os.path.join(src, name), encoding="utf-8") as f:
            for row in csv.DictReader(f):
                a, b = sorted((row["Drug_A"].strip(), row["Drug_B"].strip()))
                if a == b:
                    continue
                level = LEVELS[row["Level"].strip()]
                prev = pairs.get((a, b))
                # Keep the most severe level if the groups disagree.
                order = ["unknown", "minor", "moderate", "major"]
                if prev is None or order.index(level) > order.index(prev):
                    pairs[(a, b)] = level

    buf = io.StringIO()
    w = csv.writer(buf, lineterminator="\n")
    for (a, b), level in sorted(pairs.items()):
        w.writerow([a, b, level])
    os.makedirs(dst, exist_ok=True)
    with gzip.GzipFile(os.path.join(dst, "ddinter.csv.gz"), "wb", mtime=0) as f:
        f.write(buf.getvalue().encode("utf-8"))

    today = datetime.date.today().isoformat()
    with open(os.path.join(dst, "LICENSE-DDInter.md"), "w") as f:
        f.write(
            "# DDInter data\n\n"
            "`ddinter.csv.gz` is derived from DDInter (https://ddinter.scbdd.com), "
            "Xiong G. et al., *DDInter: an online drug-drug interaction database towards improving "
            "clinical decision-making and patient safety*, Nucleic Acids Research 2022, "
            "doi:10.1093/nar/gkab880.\n\n"
            "Licence: Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International "
            "(https://creativecommons.org/licenses/by-nc-sa/4.0/). This derived file is shared "
            "under the same licence. Changes: pairs from all ATC group files merged, deduplicated, "
            "levels lowercased.\n\n"
            f"Downloaded: {today}. Pairs: {len(pairs)}.\n"
        )
    print(f"{len(pairs)} pairs")


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
