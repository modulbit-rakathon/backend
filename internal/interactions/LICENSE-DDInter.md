# DDInter data

`ddinter.csv.gz` is derived from DDInter (https://ddinter.scbdd.com), Xiong G. et al., *DDInter: an online drug-drug interaction database towards improving clinical decision-making and patient safety*, Nucleic Acids Research 2022, doi:10.1093/nar/gkab880.

Licence: Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International (https://creativecommons.org/licenses/by-nc-sa/4.0/). This derived file is shared under the same licence. Changes: pairs from all ATC group files merged, deduplicated, levels lowercased.

Downloaded: 2026-10-16. Pairs: 234981.

`ddinter_text.csv.gz` holds the "Interaction" and "Management" texts from the DDInter pair pages (same licence, English originals, unchanged except whitespace), for major and moderate pairs between catalogue and regimen substances. Built by `scripts/ddinter_text.py`. Downloaded: 2026-10-17.
