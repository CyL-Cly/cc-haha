# Pinned Unicode declarations

These are the official [Unicode 17.0.0 Character Database files](https://www.unicode.org/Public/17.0.0/ucd/), compressed without timestamps. `sources.json` records the original URL, uncompressed size and SHA-256. `LICENSE` contains the Unicode data license.

Production normalization uses `UnicodeData.txt` and `DerivedNormalizationProps.txt`. `NormalizationTest.txt` is test input only. Runtime code makes no network request and executes no JavaScript normalization.

The Go implementation is checked against the full official normalization corpus and the original `src/utils/sanitization.ts`. Its Source comparison covers every single Unicode codepoint, including unpaired surrogate units, plus recursive metadata and key collisions. It checks actual Source character behavior; a runtime version label alone is insufficient to select normalization tables.
