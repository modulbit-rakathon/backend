#!/usr/bin/env python3
"""Split sample list photos into a printed part and a handwritten part.

Each sample sheet has the printed list on top and a handwritten copy below.
The split goes through the widest blank gap between text lines. Output goes
to data/samples/<name>_printed.jpg and _handwritten.jpg (gitignored), plus
_split.jpg previews to check the cut by eye.

Usage: scripts/split_samples.py ../Data/Seznamy_foto data/samples
"""
import os
import sys

import numpy as np
from PIL import Image, ImageDraw, ImageFilter, ImageOps


def text_rows(gray):
    """Boolean per row: does the row contain ink (dark relative to its surroundings)."""
    small = gray.resize((gray.width // 4, gray.height // 4))
    bg = small.filter(ImageFilter.BoxBlur(15))
    a, b = np.asarray(small, dtype=np.int16), np.asarray(bg, dtype=np.int16)
    ink = (b - a) > 25
    # Ignore page edges, where the table and shadows sit.
    w = ink.shape[1]
    ink = ink[:, int(w * 0.08):int(w * 0.92)]
    # Only rows on the paper: the table around it is darker and textured.
    med = np.median(a[:, int(w * 0.2):int(w * 0.8)], axis=1)
    page = med > 0.8 * np.percentile(med, 95)
    page = np.convolve(page, np.ones(15), "same") >= 15  # erode: drop the paper edge
    rows = (ink.sum(axis=1) > 2) & page
    return np.repeat(rows, 4)[: gray.height]


def split_row(rows):
    """Middle of the widest blank run between the first and last text row."""
    idx = np.flatnonzero(rows)
    if len(idx) < 2:
        return None
    gaps = np.diff(idx)
    i = int(np.argmax(gaps))
    return int((idx[i] + idx[i + 1]) // 2)


# Photos where the handwriting follows the print without a usable gap.
SKIP = {"IMG_20261015_151032541.jpg"}


def main(src, dst):
    os.makedirs(dst, exist_ok=True)
    for name in sorted(os.listdir(src)):
        if not name.lower().endswith((".jpg", ".jpeg", ".png")):
            continue
        if name in SKIP:
            print(f"{name}: skipped (no gap between printed and handwritten)")
            continue
        img = ImageOps.exif_transpose(Image.open(os.path.join(src, name))).convert("RGB")
        y = split_row(text_rows(img.convert("L")))
        if y is None:
            print(f"{name}: no text found, skipped")
            continue
        base = os.path.splitext(name)[0]
        img.crop((0, 0, img.width, y)).save(os.path.join(dst, f"{base}_printed.jpg"), quality=90)
        img.crop((0, y, img.width, img.height)).save(os.path.join(dst, f"{base}_handwritten.jpg"), quality=90)
        preview = img.resize((img.width // 6, img.height // 6))
        ImageDraw.Draw(preview).line((0, y // 6, preview.width, y // 6), fill=(255, 0, 0), width=3)
        preview.save(os.path.join(dst, f"{base}_split.jpg"))
        print(f"{name}: split at {y / img.height:.0%}")


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
