package insights

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var boldPattern = regexp.MustCompile(`\*\*(.+?)\*\*`)
var titlePattern = regexp.MustCompile(`\b\w`)

func escape(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;").Replace(text)
}
func bold(text string) string {
	return boldPattern.ReplaceAllString(escape(text), "<strong>$1</strong>")
}
func decimal(v float64) string {
	if math.IsNaN(v) {
		return "NaN"
	}
	if math.IsInf(v, 1) {
		return "Infinity"
	}
	if math.IsInf(v, -1) {
		return "-Infinity"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}
func locale(v json.RawMessage) string {
	if len(v) > 0 && v[0] == '"' {
		return jsText(v)
	}
	n := jsNumber(v)
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return decimal(n)
	}
	s := strconv.FormatFloat(n, 'f', 3, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	pieces := strings.SplitN(s, ".", 2)
	whole := pieces[0]
	sign := ""
	if strings.HasPrefix(whole, "-") {
		sign = "-"
		whole = whole[1:]
	}
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	s = sign + whole
	if len(pieces) > 1 {
		s += "." + pieces[1]
	}
	return s
}
func bars(value json.RawMessage, color string, maxItems int, order []string) string {
	rows, _ := entries(value)
	if order != nil {
		object, _ := object(value)
		rows = nil
		for _, key := range order {
			v := object.Get(key)
			if jsNumber(v) > 0 {
				rows = append(rows, protocol.JSONProperty{NameJSON: raw(key), Value: v})
			}
		}
	} else {
		sort.SliceStable(rows, func(i, j int) bool { return jsNumber(rows[i].Value) > jsNumber(rows[j].Value) })
		if len(rows) > maxItems {
			rows = rows[:maxItems]
		}
	}
	if len(rows) == 0 {
		return `<p class="empty">No data</p>`
	}
	maximum := math.Inf(-1)
	for _, row := range rows {
		maximum = math.Max(maximum, jsNumber(row.Value))
	}
	lines := []string{}
	for _, row := range rows {
		name := jsText(row.NameJSON)
		label := assets.Labels[name]
		if label == "" {
			label = titlePattern.ReplaceAllStringFunc(strings.ReplaceAll(name, "_", " "), strings.ToUpper)
		}
		lines = append(lines, interpolate(assets.Bars[0], escape(label), decimal(jsNumber(row.Value)/maximum*100), color, jsText(row.Value)))
	}
	return strings.Join(lines, "\n")
}
func histogram(value json.RawMessage) string {
	var times []float64
	_ = json.Unmarshal(value, &times)
	if len(times) == 0 {
		return `<p class="empty">No response time data</p>`
	}
	names := []string{"2-10s", "10-30s", "30s-1m", "1-2m", "2-5m", "5-15m", ">15m"}
	limits := []float64{10, 30, 60, 120, 300, 900}
	counts := make([]int, 7)
	for _, n := range times {
		i := 0
		for i < len(limits) && !(n < limits[i]) {
			i++
		}
		counts[i]++
	}
	maximum := 0
	for _, n := range counts {
		maximum = max(maximum, n)
	}
	lines := []string{}
	for i, n := range counts {
		lines = append(lines, interpolate(assets.Response[0], names[i], decimal(float64(n)/float64(maximum)*100), strconv.Itoa(n)))
	}
	return strings.Join(lines, "\n")
}
func hourCounts(value json.RawMessage) protocol.JSONObject {
	var hours []json.RawMessage
	_ = json.Unmarshal(value, &hours)
	counts := protocol.NewJSONObject()
	for _, hour := range hours {
		key := jsText(hour)
		counts.Set(key, number(jsNumberOrZero(counts.Get(key))+1))
	}
	return counts
}
func jsNumberOrZero(v json.RawMessage) float64 {
	if !truthy(v) {
		return 0
	}
	return jsNumber(v)
}
func hoursChart(value json.RawMessage) string {
	var hours []json.RawMessage
	_ = json.Unmarshal(value, &hours)
	if len(hours) == 0 {
		return `<p class="empty">No time data</p>`
	}
	counts := hourCounts(value)
	names := []string{"Morning (6-12)", "Afternoon (12-18)", "Evening (18-24)", "Night (0-6)"}
	sums := make([]float64, 4)
	starts := []int{6, 12, 18, 0}
	maximum := float64(1)
	for i, start := range starts {
		for h := start; h < start+6; h++ {
			sums[i] += jsNumberOrZero(counts.Get(strconv.Itoa(h)))
		}
		maximum = math.Max(maximum, sums[i])
	}
	lines := []string{}
	for i, n := range sums {
		lines = append(lines, interpolate(assets.Hours[0], names[i], decimal(n/maximum*100), decimal(n)))
	}
	return interpolate(assets.Hours[2], strings.Join(lines, "\n"))
}

type htmlRenderer struct{ err error }

func (r *htmlRenderer) text(o protocol.JSONObject, key string, fallback bool) string {
	v := o.Get(key)
	if fallback && !truthy(v) {
		return ""
	}
	text, err := requiredString(v)
	if err != nil {
		r.err = err
	}
	return escape(text)
}
func (r *htmlRenderer) rows(o protocol.JSONObject, key string) []protocol.JSONObject {
	v := o.Get(key)
	if !truthy(v) {
		return nil
	}
	var rows []protocol.JSONObject
	if json.Unmarshal(v, &rows) != nil {
		r.err = errors.New("Insights HTML list is not an array")
	}
	return rows
}
func (r *htmlRenderer) conditional(o protocol.JSONObject, key string, index int) string {
	if !truthy(o.Get(key)) {
		return ""
	}
	return interpolate(assets.HTML[index], r.text(o, key, false))
}
func markdown(text string) string {
	paragraphs := strings.Split(text, "\n\n")
	for i, p := range paragraphs {
		html := bold(p)
		html = regexp.MustCompile(`(?m)^- `).ReplaceAllString(html, "• ")
		html = strings.ReplaceAll(html, "\n", "<br>")
		paragraphs[i] = "<p>" + html + "</p>"
	}
	return strings.Join(paragraphs, "\n")
}

// HTML renders the original report text/assets with native Go chart and
// section producers. Embedded JavaScript is only the report's UI interaction.
func HTML(data, insights protocol.JSONObject) (string, error) {
	r := htmlRenderer{}
	h := func(i int, values ...string) string { return interpolate(assets.HTML[i], values...) }
	at, _ := insights.Object("at_a_glance")
	glance := ""
	if truthy(insights.Get("at_a_glance")) {
		values := []string{}
		for i, key := range []string{"whats_working", "whats_hindering", "quick_wins", "ambitious_workflows"} {
			value := ""
			if truthy(at.Get(key)) {
				text, err := requiredString(at.Get(key))
				if err != nil {
					r.err = err
				}
				value = h(4+i, bold(text))
			}
			values = append(values, value)
		}
		glance = h(3, values...)
	}
	project, _ := insights.Object("project_areas")
	areas := r.rows(project, "areas")
	projectHTML := ""
	if len(areas) > 0 {
		items := []string{}
		for _, a := range areas {
			items = append(items, h(9, r.text(a, "name", false), jsText(a.Get("session_count")), r.text(a, "description", false)))
		}
		projectHTML = h(8, strings.Join(items, ""))
	}
	interaction, _ := insights.Object("interaction_style")
	interactionHTML := ""
	if truthy(interaction.Get("narrative")) {
		text, err := requiredString(interaction.Get("narrative"))
		if err != nil {
			r.err = err
		}
		interactionHTML = h(10, markdown(text), r.conditional(interaction, "key_pattern", 11))
	}
	works, _ := insights.Object("what_works")
	wins := r.rows(works, "impressive_workflows")
	worksHTML := ""
	if len(wins) > 0 {
		items := []string{}
		for _, w := range wins {
			items = append(items, h(14, r.text(w, "title", true), r.text(w, "description", true)))
		}
		worksHTML = h(12, r.conditional(works, "intro", 13), strings.Join(items, ""))
	}
	friction, _ := insights.Object("friction_analysis")
	categories := r.rows(friction, "categories")
	frictionHTML := ""
	if len(categories) > 0 {
		items := []string{}
		for _, c := range categories {
			examples := ""
			if truthy(c.Get("examples")) {
				var list []json.RawMessage
				if json.Unmarshal(c.Get("examples"), &list) != nil {
					r.err = errors.New("Insights examples is not an array")
				}
				e := []string{}
				for _, v := range list {
					text, err := requiredString(v)
					if err != nil {
						r.err = err
					}
					e = append(e, h(19, escape(text)))
				}
				examples = h(18, strings.Join(e, ""))
			}
			items = append(items, h(17, r.text(c, "category", true), r.text(c, "description", true), examples))
		}
		frictionHTML = h(15, r.conditional(friction, "intro", 16), strings.Join(items, ""))
	}
	suggestions, _ := insights.Object("suggestions")
	suggestionsHTML := ""
	if truthy(insights.Get("suggestions")) {
		additionHTML, featuresHTML, patternsHTML := "", "", ""
		if rows := r.rows(suggestions, "claude_md_additions"); len(rows) > 0 {
			items := []string{}
			for i, a := range rows {
				where := a.Get("prompt_scaffold")
				if !truthy(where) {
					where = a.Get("where")
				}
				if !truthy(where) {
					where = raw("Add to CLAUDE.md")
				}
				whereText, err := requiredString(where)
				if err != nil {
					r.err = err
				}
				index := strconv.Itoa(i)
				items = append(items, h(22, index, escape(whereText), r.text(a, "addition", false), index, r.text(a, "addition", false), index, r.text(a, "why", false)))
			}
			additionHTML = h(21, strings.Join(items, ""))
		}
		if rows := r.rows(suggestions, "features_to_try"); len(rows) > 0 {
			items := []string{}
			for _, f := range rows {
				items = append(items, h(24, r.text(f, "feature", true), r.text(f, "one_liner", true), r.text(f, "why_for_you", true), r.conditional(f, "example_code", 25)))
			}
			featuresHTML = h(23, strings.Join(items, ""))
		}
		if rows := r.rows(suggestions, "usage_patterns"); len(rows) > 0 {
			items := []string{}
			for _, p := range rows {
				items = append(items, h(27, r.text(p, "title", true), r.text(p, "suggestion", true), r.conditional(p, "detail", 28), r.conditional(p, "copyable_prompt", 29)))
			}
			patternsHTML = h(26, strings.Join(items, ""))
		}
		suggestionsHTML = h(20, additionHTML, featuresHTML, patternsHTML)
	}
	horizon, _ := insights.Object("on_the_horizon")
	opportunities := r.rows(horizon, "opportunities")
	horizonHTML := ""
	if len(opportunities) > 0 {
		items := []string{}
		for _, o := range opportunities {
			items = append(items, h(32, r.text(o, "title", true), r.text(o, "whats_possible", true), r.conditional(o, "how_to_try", 33), r.conditional(o, "copyable_prompt", 34)))
		}
		horizonHTML = h(30, r.conditional(horizon, "intro", 31), strings.Join(items, ""))
	}
	fun, _ := insights.Object("fun_ending")
	funHTML := ""
	if truthy(fun.Get("headline")) {
		funHTML = h(42, r.text(fun, "headline", false), r.conditional(fun, "detail", 43))
	}
	date, _ := data.Object("date_range")
	scanned := ""
	if truthy(data.Get("total_sessions_scanned")) && jsNumber(data.Get("total_sessions_scanned")) > jsNumber(data.Get("total_sessions")) {
		scanned = h(47, locale(data.Get("total_sessions_scanned")))
	}
	multi, _ := data.Object("multi_clauding")
	multiHTML := h(48)
	if jsNumber(multi.Get("overlap_events")) != 0 {
		percentage := float64(0)
		if jsNumber(data.Get("total_messages")) > 0 {
			percentage = math.Floor(100*jsNumber(multi.Get("user_messages_during"))/jsNumber(data.Get("total_messages")) + 0.5)
		}
		multiHTML = h(49, jsText(multi.Get("overlap_events")), jsText(multi.Get("sessions_involved")), decimal(percentage))
	}
	toolErrorHTML := `<p class="empty">No tool errors</p>`
	if rows, _ := entries(data.Get("tool_error_categories")); len(rows) > 0 {
		toolErrorHTML = bars(data.Get("tool_error_categories"), "#dc2626", 6, nil)
	}
	js := h(45, string(raw(hourCounts(data.Get("message_hours")))))
	values := []string{h(44), locale(data.Get("total_messages")), jsText(data.Get("total_sessions")), scanned, jsText(date.Get("start")), jsText(date.Get("end")), glance, locale(data.Get("total_messages")), locale(data.Get("total_lines_added")), locale(data.Get("total_lines_removed")), jsText(data.Get("total_files_modified")), jsText(data.Get("days_active")), jsText(data.Get("messages_per_day")), projectHTML, bars(data.Get("goal_categories"), "#2563eb", 6, nil), bars(data.Get("tool_counts"), "#0891b2", 6, nil), bars(data.Get("languages"), "#10b981", 6, nil), bars(data.Get("session_types"), "#8b5cf6", 6, nil), interactionHTML, histogram(data.Get("user_response_times")), fmt.Sprintf("%.1f", jsNumber(data.Get("median_response_time"))), fmt.Sprintf("%.1f", jsNumber(data.Get("avg_response_time"))), multiHTML, hoursChart(data.Get("message_hours")), toolErrorHTML, worksHTML, bars(data.Get("success"), "#16a34a", 6, nil), bars(data.Get("outcomes"), "#8b5cf6", 6, assets.Outcomes), frictionHTML, bars(data.Get("friction"), "#dc2626", 6, nil), bars(data.Get("satisfaction"), "#eab308", 6, assets.Satisfaction), suggestionsHTML, horizonHTML, funHTML, "", js}
	if r.err != nil {
		return "", r.err
	}
	return h(46, values...), nil
}
