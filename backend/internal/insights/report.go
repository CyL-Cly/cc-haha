package insights

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type SessionFile struct {
	SessionID, Path string
	Mtime           float64
	Size            int64
}
type ReportOptions struct {
	Cache      *Cache
	Models     *Models
	Stats      StatsOptions
	Scan       func(context.Context) ([]SessionFile, error)
	Load       func(context.Context, string) ([]Log, error)
	Compare    func(string, string) (int, error)
	AwaitFiles func(context.Context, func() ([]byte, error)) ([]byte, error)
}
type Report struct {
	Insights, Data protocol.JSONObject
	HTMLPath       string
	Facets         []protocol.JSONObject
}
type Reports struct{ options ReportOptions }

func NewReports(o ReportOptions) (*Reports, error) {
	if o.Cache == nil || o.Models == nil || o.Scan == nil || o.Load == nil || o.Compare == nil || o.Stats.ParseDate == nil || o.AwaitFiles == nil {
		return nil, errors.New("Insights report requires shared cache, models, session history, date, and collation")
	}
	return &Reports{o}, nil
}
func metaSession(log Log) bool {
	for _, record := range log.Messages[:min(5, len(log.Messages))] {
		if record.String("type") != "user" {
			continue
		}
		body, ok := record.Object("message")
		if !ok {
			continue
		}
		content := body.Get("content")
		if len(content) == 0 || content[0] != '"' {
			continue
		}
		text := jsText(content)
		if strings.Contains(text, "RESPOND WITH ONLY A VALID JSON OBJECT") || strings.Contains(text, "record_facets") {
			return true
		}
	}
	return false
}
func minimal(f protocol.JSONObject) bool {
	rows, _ := entries(f.Get("goal_categories"))
	positive := []string{}
	for _, row := range rows {
		if jsNumber(row.Value) > 0 {
			positive = append(positive, jsText(row.NameJSON))
		}
	}
	return len(positive) == 1 && positive[0] == "warmup_minimal"
}
func (r *Reports) Generate(ctx context.Context) (Report, error) {
	o := r.options
	scanned, err := o.Scan(ctx)
	if err != nil {
		return Report{}, err
	}
	metas := []protocol.JSONObject{}
	uncached := []SessionFile{}
	for start := 0; start < len(scanned); start += 50 {
		batch := scanned[start:min(start+50, len(scanned))]
		ids := []string{}
		for _, file := range batch {
			ids = append(ids, file.SessionID)
		}
		data, e := o.AwaitFiles(ctx, func() ([]byte, error) { return raw(o.Cache.loadBatch(ids, false)), nil })
		if e != nil {
			return Report{}, e
		}
		var cachedValues []json.RawMessage
		if e = json.Unmarshal(data, &cachedValues); e != nil {
			return Report{}, e
		}
		for i, file := range batch {
			cached := cachedValues[i]
			if truthy(cached) {
				meta, err := object(cached)
				if err != nil {
					return Report{}, err
				}
				metas = append(metas, meta)
			} else if len(uncached) < 200 {
				uncached = append(uncached, file)
			}
		}
	}
	logs := map[string]Log{}
	for start := 0; start < len(uncached); start += 10 {
		toSave := []protocol.JSONObject{}
		for _, file := range uncached[start:min(start+10, len(uncached))] {
			branches, err := o.Load(ctx, file.Path)
			if err != nil {
				continue
			}
			for _, log := range branches {
				if metaSession(log) || math.IsNaN(log.CreatedMillis) || math.IsNaN(log.ModifiedMillis) {
					continue
				}
				meta, err := LogMeta(log, o.Stats)
				if err != nil {
					return Report{}, err
				}
				metas = append(metas, meta)
				toSave = append(toSave, meta)
				logs[meta.String("session_id")] = log
			}
		}
		if _, err = o.AwaitFiles(ctx, func() ([]byte, error) { return nil, o.Cache.saveBatch(toSave, false) }); err != nil {
			return Report{}, err
		}
	}
	metas = Deduplicate(metas)
	var compareErr error
	sort.SliceStable(metas, func(i, j int) bool {
		c, e := o.Compare(metas[j].String("start_time"), metas[i].String("start_time"))
		if e != nil {
			compareErr = e
		}
		return c < 0
	})
	if compareErr != nil {
		return Report{}, compareErr
	}
	substantive := []protocol.JSONObject{}
	for _, meta := range metas {
		if jsNumber(meta.Get("user_message_count")) < 2 || jsNumber(meta.Get("duration_minutes")) < 1 {
			continue
		}
		substantive = append(substantive, meta)
	}
	facets := []protocol.JSONObject{}
	positions := map[string]int{}
	extract := []Log{}
	put := func(id string, facet protocol.JSONObject) {
		if i, exists := positions[id]; exists {
			facets[i] = facet
			return
		}
		positions[id] = len(facets)
		facets = append(facets, facet)
	}
	ids := []string{}
	for _, meta := range substantive {
		ids = append(ids, meta.String("session_id"))
	}
	cacheData, err := o.AwaitFiles(ctx, func() ([]byte, error) { return raw(o.Cache.loadBatch(ids, true)), nil })
	if err != nil {
		return Report{}, err
	}
	var cachedFacets []json.RawMessage
	if err = json.Unmarshal(cacheData, &cachedFacets); err != nil {
		return Report{}, err
	}
	for index, meta := range substantive {
		id := meta.String("session_id")
		cached := cachedFacets[index]
		if truthy(cached) {
			facet, _ := object(cached)
			put(id, facet)
		} else if log, ok := logs[id]; ok && len(extract) < 50 {
			extract = append(extract, log)
		}
	}
	if len(extract) > 0 {
		fresh, err := o.Models.Facets(ctx, extract)
		if err != nil {
			return Report{}, err
		}
		for i, facet := range fresh {
			if facet == nil {
				continue
			}
			put(extract[i].SessionID, *facet)
		}
		toSave := []protocol.JSONObject{}
		for _, facet := range fresh {
			if facet != nil {
				toSave = append(toSave, *facet)
			}
		}
		if _, err = o.AwaitFiles(ctx, func() ([]byte, error) { return nil, o.Cache.saveBatch(toSave, true) }); err != nil {
			return Report{}, err
		}
	}
	kept := []protocol.JSONObject{}
	for _, meta := range substantive {
		id := meta.String("session_id")
		if i, ok := positions[id]; ok && minimal(facets[i]) {
			continue
		}
		kept = append(kept, meta)
	}
	keptFacets := []protocol.JSONObject{}
	facetMap := map[string]protocol.JSONObject{}
	for id, i := range positions {
		if !minimal(facets[i]) {
			facetMap[id] = facets[i]
		}
	}
	for _, facet := range facets {
		if !minimal(facet) {
			keptFacets = append(keptFacets, facet)
		}
	}
	data, err := Aggregate(kept, facetMap, o.Stats.ParseDate)
	if err != nil {
		return Report{}, err
	}
	data.Set("total_sessions_scanned", raw(len(scanned)))
	insights, err := o.Models.Narrative(ctx, data, facets)
	if err != nil {
		return Report{}, err
	}
	html, err := HTML(data, insights)
	if err != nil {
		return Report{}, err
	}
	_ = o.Cache.files.MkdirAll(o.Cache.DataDir(), 0777)
	path := filepath.Join(o.Cache.DataDir(), "report.html")
	if err = o.Cache.files.WriteFile(path, []byte(html), 0600); err != nil {
		return Report{}, err
	}
	return Report{Insights: insights, Data: data, HTMLPath: path, Facets: keptFacets}, nil
}

func CommandPrompt(report Report, facetsDirectory string) string {
	data := report.Data
	insights := report.Insights
	url := "file://" + report.HTMLPath
	sessionLabel := jsText(data.Get("total_sessions")) + " sessions"
	if truthy(data.Get("total_sessions_scanned")) && jsNumber(data.Get("total_sessions_scanned")) > jsNumber(data.Get("total_sessions")) {
		sessionLabel = locale(data.Get("total_sessions_scanned")) + " sessions total · " + jsText(data.Get("total_sessions")) + " analyzed"
	}
	stats := sessionLabel + " · " + locale(data.Get("total_messages")) + " messages · " + decimal(math.Floor(jsNumber(data.Get("total_duration_hours"))+0.5)) + "h · " + jsText(data.Get("git_commits")) + " commits"
	summary := "_No insights generated_"
	if truthy(insights.Get("at_a_glance")) {
		glance, _ := insights.Object("at_a_glance")
		parts := []string{"## At a Glance\n\n"}
		for _, row := range [][3]string{{"whats_working", "What's working", "Impressive Things You Did"}, {"whats_hindering", "What's hindering you", "Where Things Go Wrong"}, {"quick_wins", "Quick wins to try", "Features to Try"}, {"ambitious_workflows", "Ambitious workflows", "On the Horizon"}} {
			text := ""
			if truthy(glance.Get(row[0])) {
				text = "**" + row[1] + ":** " + jsText(glance.Get(row[0])) + " See _" + row[2] + "_."
			}
			parts = append(parts, text, "\n\n")
		}
		summary = strings.TrimSuffix(strings.Join(parts, ""), "\n\n")
	}
	date, _ := data.Object("date_range")
	userSummary := "# Claude Code Insights\n\n" + stats + "\n" + jsText(date.Get("start")) + " to " + jsText(date.Get("end")) + "\n\n" + summary + "\n\nYour full shareable insights report is ready: " + url
	return "The user just ran /insights to generate a usage report analyzing their Claude Code sessions.\n\nHere is the full insights data:\n" + pretty(insights) + "\n\nReport URL: " + url + "\nHTML file: " + report.HTMLPath + "\nFacets directory: " + facetsDirectory + "\n\nHere is what the user sees:\n" + userSummary + "\n\nNow output the following message exactly:\n\n<message>\nYour shareable insights report is ready:\n" + url + "\n\nWant to dig into any section or try one of the suggestions?\n</message>"
}
