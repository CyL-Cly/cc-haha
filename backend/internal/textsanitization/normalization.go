package textsanitization

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
)

// Unicode data is the official versioned character/normalization declaration,
// not an oracle captured from an Agent or the old TypeScript implementation.
// The Source runtime's reported process.versions.unicode differs from its
// actual character behavior; the exhaustive Source test checks that behavior.
//
//go:embed data/UnicodeData.txt.gz
var unicodeData []byte

//go:embed data/DerivedNormalizationProps.txt.gz
var normalizationProperties []byte

type unicodeTables struct {
	allowed        [0x110000]bool
	classes        map[rune]uint8
	decompositions map[rune][]rune
	compositions   map[uint64]rune
}

func officialFile(compressed []byte, expected string) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, 4*1024*1024))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != expected {
		return nil, errors.New("pinned Unicode declaration checksum mismatch")
	}
	return data, nil
}
func runeCode(text string) (rune, error) {
	value, err := strconv.ParseInt(text, 16, 32)
	return rune(value), err
}

var tables = sync.OnceValues(func() (*unicodeTables, error) {
	data, err := officialFile(unicodeData, "2e1efc1dcb59c575eedf5ccae60f95229f706ee6d031835247d843c11d96470c")
	if err != nil {
		return nil, err
	}
	properties, err := officialFile(normalizationProperties, "71fd6a206a2c0cdd41feb6b7f656aa31091db45e9cedc926985d718397f9e488")
	if err != nil {
		return nil, err
	}
	excluded := map[rune]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(properties))
	for scanner.Scan() {
		line := strings.SplitN(scanner.Text(), "#", 2)[0]
		parts := strings.Split(line, ";")
		if len(parts) < 2 || strings.TrimSpace(parts[1]) != "Full_Composition_Exclusion" {
			continue
		}
		span := strings.Split(strings.TrimSpace(parts[0]), "..")
		first, err := runeCode(span[0])
		if err != nil {
			return nil, err
		}
		last := first
		if len(span) > 1 {
			last, err = runeCode(span[1])
			if err != nil {
				return nil, err
			}
		}
		for r := first; r <= last; r++ {
			excluded[r] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	t := &unicodeTables{classes: map[rune]uint8{}, decompositions: map[rune][]rune{}, compositions: map[uint64]rune{}}
	first := rune(-1)
	scanner = bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ";")
		if len(fields) != 15 {
			return nil, errors.New("invalid official UnicodeData declaration")
		}
		r, err := runeCode(fields[0])
		if err != nil {
			return nil, err
		}
		if r < 0 || r > 0x10ffff {
			return nil, errors.New("invalid Unicode codepoint")
		}
		if strings.HasSuffix(fields[1], ", First>") {
			first = r
			continue
		}
		start := r
		if strings.HasSuffix(fields[1], ", Last>") {
			if first < 0 {
				return nil, errors.New("invalid Unicode range declaration")
			}
			start, first = first, -1
		}
		allowed := fields[2] != "Cf" && fields[2] != "Co" && fields[2] != "Cn"
		for code := start; code <= r; code++ {
			t.allowed[code] = allowed
		}
		class, err := strconv.ParseUint(fields[3], 10, 8)
		if err != nil {
			return nil, err
		}
		if class > 0 {
			t.classes[r] = uint8(class)
		}
		mapping := strings.Fields(fields[5])
		compatibility := len(mapping) > 0 && strings.HasPrefix(mapping[0], "<")
		if compatibility {
			mapping = mapping[1:]
		}
		if len(mapping) > 0 {
			decomposition := make([]rune, len(mapping))
			for i, part := range mapping {
				decomposition[i], err = runeCode(part)
				if err != nil {
					return nil, err
				}
			}
			t.decompositions[r] = decomposition
			if !compatibility && len(decomposition) == 2 && !excluded[r] {
				t.compositions[compositionKey(decomposition[0], decomposition[1])] = r
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if first >= 0 {
		return nil, errors.New("unterminated Unicode range")
	}
	return t, nil
})

func compositionKey(first, second rune) uint64 { return uint64(first)<<21 | uint64(second) }
func (t *unicodeTables) decompose(r rune, out *[]rune) {
	// Algorithmic Hangul decomposition is defined separately from UnicodeData.
	if r >= 0xac00 && r < 0xac00+11172 {
		index := r - 0xac00
		*out = append(*out, 0x1100+index/588, 0x1161+index%588/28)
		if tail := index % 28; tail > 0 {
			*out = append(*out, 0x11a7+tail)
		}
		return
	}
	if mapping, ok := t.decompositions[r]; ok {
		for _, child := range mapping {
			t.decompose(child, out)
		}
		return
	}
	*out = append(*out, r)
}
func (t *unicodeTables) compose(first, second rune) (rune, bool) {
	if first >= 0x1100 && first < 0x1100+19 && second >= 0x1161 && second < 0x1161+21 {
		return 0xac00 + (first-0x1100)*588 + (second-0x1161)*28, true
	}
	if first >= 0xac00 && first < 0xac00+11172 && (first-0xac00)%28 == 0 && second > 0x11a7 && second < 0x11a7+28 {
		return first + second - 0x11a7, true
	}
	r, ok := t.compositions[compositionKey(first, second)]
	return r, ok
}
func (t *unicodeTables) nfkc(input []rune) []rune {
	decomposed := make([]rune, 0, len(input))
	for _, r := range input {
		t.decompose(r, &decomposed)
	}
	for at := 1; at < len(decomposed); at++ {
		class := t.classes[decomposed[at]]
		if class == 0 {
			continue
		}
		for pos := at; pos > 0 && t.classes[decomposed[pos-1]] > class; pos-- {
			decomposed[pos], decomposed[pos-1] = decomposed[pos-1], decomposed[pos]
		}
	}
	if len(decomposed) == 0 {
		return decomposed
	}
	result := []rune{decomposed[0]}
	starterPos, starter, lastClass := 0, decomposed[0], t.classes[decomposed[0]]
	if lastClass != 0 {
		starterPos = -1
	}
	for _, r := range decomposed[1:] {
		class := t.classes[r]
		composite, ok := t.compose(starter, r)
		if starterPos >= 0 && ok && (lastClass == 0 || lastClass < class) {
			result[starterPos], starter = composite, composite
			continue
		}
		if class == 0 {
			starterPos, starter = len(result), r
		}
		result = append(result, r)
		lastClass = class
	}
	return result
}
