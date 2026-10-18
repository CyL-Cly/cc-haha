# Pinned Unicode declarations

These are the official [Unicode 17.0.0 Character Database files](https://www.unicode.org/Public/17.0.0/ucd/), compressed without timestamps. `sources.json` records the original URL, uncompressed size and SHA-256. `LICENSE` contains the Unicode data license.

Character-category filtering and portable normalization use `UnicodeData.txt` and `DerivedNormalizationProps.txt`. `NormalizationTest.txt` is test input only. Runtime code makes no network request and executes no JavaScript normalization.

On macOS, the pinned Bun 1.3.14 executable dynamically imports `unorm2_getNFKCInstance` and `unorm2_normalize` from `/usr/lib/libicucore.A.dylib`. The Go sanitizer uses those same native ICU functions, independently of the Unicode17 character-category declarations. This matters on macOS 26.3: actual host ICU76.1/Unicode16 normalization preserves U+A7F1, while the Source regex engine recognizes it as assigned. Bun's build-time `process.versions` reports ICU74.2/Unicode15.1 and cannot select both tables by itself. The exhaustive Source comparison and official Unicode17 normalization corpus both pass on this host. Linux/Windows Source normalization profiles require their own native evidence.

The Go implementation is checked against the full official normalization corpus and the original `src/utils/sanitization.ts`. Its Source comparison covers every single Unicode codepoint, including unpaired surrogate units, plus recursive metadata and key collisions. It checks actual Source character behavior; a runtime version label alone is insufficient to select normalization tables.
