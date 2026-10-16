#!/usr/bin/env python3
"""Turn the organisers' 50_Pacientu.docx (fictional patients) into data/ground_truth.json.

Line format: "NAME, strength, frequency (Alternative brand)". Czech decimal
commas ("0,5 g") have no space after them, so fields split on ", ".

Usage: scripts/ground_truth.py ../Data/50_Pacientu.docx data/ground_truth.json
"""
import json
import re
import sys
import zipfile


def paragraphs(path):
    xml = zipfile.ZipFile(path).read("word/document.xml").decode()
    for p in re.findall(r"<w:p[ >].*?</w:p>", xml, flags=re.S):
        text = "".join(re.findall(r"<w:t[^>]*>([^<]*)</w:t>", p)).strip()
        if text:
            yield text


def parse(line):
    alt = None
    m = re.search(r"\s*\(([^()]*)\)\s*$", line)
    if m:
        alt, line = m.group(1).strip(), line[: m.start()]
    parts = [p.strip() for p in line.split(", ")]
    return {
        "name": parts[0],
        "alternative": alt,
        "strength": parts[1] if len(parts) > 1 else None,
        "frequency": ", ".join(parts[2:]) or None,
    }


def main(src, dst):
    patients, current = {}, None
    for text in paragraphs(src):
        m = re.fullmatch(r"Pacient\s+(\d+)", text)
        if m:
            current = m.group(1)
            patients[current] = []
        elif current:
            patients[current].append(parse(text))
    with open(dst, "w") as f:
        json.dump(patients, f, ensure_ascii=False, indent=1)
    print(f"{len(patients)} patients, {sum(map(len, patients.values()))} items")


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
