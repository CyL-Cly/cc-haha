package insights

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// reportAsset contains only original text, styles, browser interaction scripts,
// and prompt declarations. Scheduling, parsing, and HTML rendering run in Go.
//
//go:embed assets/report.json
var reportAsset []byte

type section struct {
	Name, Prompt string
	MaxTokens    float64
}
type assetData struct {
	Facet        string            `json:"FACET_EXTRACTION_PROMPT"`
	Summary      string            `json:"SUMMARIZE_CHUNK_PROMPT"`
	Sections     []section         `json:"INSIGHT_SECTIONS"`
	Labels       map[string]string `json:"LABEL_MAP"`
	Outcomes     []string          `json:"OUTCOME_ORDER"`
	Satisfaction []string          `json:"SATISFACTION_ORDER"`
	Glance       []string          `json:"at_a_glance"`
	HTML         [][]string        `json:"generateHtmlReport"`
	Bars         [][]string        `json:"generateBarChart"`
	Response     [][]string        `json:"generateResponseTimeHistogram"`
	Hours        [][]string        `json:"generateTimeOfDayChart"`
}

var assets = func() assetData {
	var a assetData
	if err := json.Unmarshal(reportAsset, &a); err != nil {
		panic(err)
	}
	return a
}()

func interpolate(parts []string, values ...string) string {
	if len(parts) != len(values)+1 {
		panic(fmt.Sprintf("Insights template arity: %d parts, %d values", len(parts), len(values)))
	}
	var b strings.Builder
	for i, part := range parts {
		b.WriteString(part)
		if i < len(values) {
			b.WriteString(values[i])
		}
	}
	return b.String()
}
