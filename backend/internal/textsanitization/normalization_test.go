package textsanitization

import (
	"bufio"
	"bytes"
	_ "embed"
	"slices"
	"strings"
	"testing"
)

//go:embed data/NormalizationTest.txt.gz
var officialNormalizationTests []byte

func TestOfficialUnicode17NormalizationConformance(t *testing.T) {
	table, err := tables()
	if err != nil {
		t.Fatal(err)
	}
	data, err := officialFile(officialNormalizationTests, "5019ffd530751a741900c849c0e010332f142a3612234639bd200b82138a87db")
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	rows := 0
	for scanner.Scan() {
		line := strings.SplitN(scanner.Text(), "#", 2)[0]
		fields := strings.Split(line, ";")
		if len(fields) < 5 {
			continue
		}
		rows++
		columns := make([][]rune, 5)
		for i, field := range fields[:5] {
			for _, code := range strings.Fields(field) {
				r, err := runeCode(code)
				if err != nil {
					t.Fatal(err)
				}
				columns[i] = append(columns[i], r)
			}
		}
		for i, input := range columns {
			got := table.nfkc(input)
			if !slices.Equal(got, columns[3]) {
				t.Fatalf("official NFKC row %d column %d input=%x got=%x expected=%x", rows, i+1, input, got, columns[3])
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if rows < 19000 {
		t.Fatalf("normalization corpus incomplete: %d", rows)
	}
}
