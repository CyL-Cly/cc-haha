package agentsapi

import (
	"golang.org/x/text/collate"
	"sort"
)

func sortNames(names []string, comparer *collate.Collator) {
	sort.SliceStable(names, func(i, j int) bool { return comparer.CompareString(names[i], names[j]) < 0 })
}
