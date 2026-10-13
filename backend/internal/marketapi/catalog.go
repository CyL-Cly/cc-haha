package marketapi

import (
	"context"
	_ "embed"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// The owning registries remain authoritative for install bytes. This frozen
// editorial snapshot only provides the offline home list and its provenance.
//
//go:embed catalog/skills.json
var catalogBytes []byte

var catalog = func() object {
	var v object
	if err := json.Unmarshal(catalogBytes, &v); err != nil {
		panic(err)
	}
	return v
}()
var ownerPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
var pipelineChangelog = regexp.MustCompile(`(?i)^synced by .*pipeline$`)

type marketOwnerKey struct{}
type ownerPin struct{ slug, owner string }

func parseOwner(v any, source string) (string, error) {
	if v == nil || v == "" {
		return "", nil
	}
	value, ok := v.(string)
	if !ok || !ownerPattern.MatchString(value) {
		return "", fail(400, "BAD_REQUEST", "Invalid owner")
	}
	if source != "clawhub" {
		return "", nil
	}
	return value, nil
}
func ownerContext(ctx context.Context, source, slug string, owner []string) (context.Context, string) {
	if source == "clawhub" && len(owner) > 0 && owner[0] != "" {
		return context.WithValue(ctx, marketOwnerKey{}, ownerPin{slug, owner[0]}), "@" + owner[0]
	}
	if pin, ok := ctx.Value(marketOwnerKey{}).(ownerPin); ok && pin.slug == slug {
		return ctx, "@" + pin.owner
	}
	return ctx, ""
}
func requestedOwner(ctx context.Context, slug string) string {
	if pin, ok := ctx.Value(marketOwnerKey{}).(ownerPin); ok && pin.slug == slug {
		return pin.owner
	}
	return ""
}
func meaningfulChangelog(v any) string {
	text := strings.TrimSpace(str(v))
	if pipelineChangelog.MatchString(text) {
		return ""
	}
	return text
}
func catalogEntry(source, slug string) object {
	for _, v := range arr(catalog["skills"]) {
		e := obj(v)
		if e["source"] == source && e["slug"] == slug {
			return e
		}
	}
	return nil
}
func catalogSkill(e object) object {
	out := object{"id": str(e["source"]) + ":" + str(e["slug"]), "source": e["source"], "slug": e["slug"], "name": e["name"], "summary": e["summary"], "author": object{"handle": e["owner"]}, "stats": clone(obj(e["stats"])), "tags": append([]any{}, arr(e["tags"])...), "category": e["category"], "securityStatus": e["security"], "curated": true, "installState": "installable"}
	for _, k := range []string{"summaryEn", "version", "updatedAt", "iconUrl", "securityNote", "requiresApiKey"} {
		optional(out, k, e[k])
	}
	if e["featured"] == true {
		out["featured"] = true
	}
	return out
}
func (s *Service) markCurated(skill object, owner string, detail bool) object {
	e := catalogEntry(str(skill["source"]), str(skill["slug"]))
	if e == nil {
		return skill
	}
	if skill["source"] == "clawhub" {
		resolved := first(owner, obj(skill["author"])["handle"])
		if (!detail || resolved != "") && resolved != e["owner"] {
			return skill
		}
	}
	out := clone(skill)
	out["curated"], out["category"] = true, e["category"]
	if e["featured"] == true {
		out["featured"] = true
	}
	if detail {
		if text := first(out["summary"], e["summaryEn"]); text != "" {
			out["summaryEn"] = text
		}
		out["summary"] = first(e["summary"], out["summary"])
		if len(arr(out["tags"])) == 0 {
			out["tags"] = append([]any{}, arr(e["tags"])...)
		}
		if out["securityStatus"] == "flagged" {
			optional(out, "securityNote", e["securityNote"])
		}
	}
	return out
}
func foldCatalog(v string) string { return strings.ToLower(norm.NFKC.String(v)) }
func (s *Service) listCatalog(ctx context.Context, p ListParams) (object, error) {
	strong, weak := []object{}, []object{}
	terms := strings.Fields(foldCatalog(p.Q))
	for _, v := range arr(catalog["skills"]) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e := obj(v)
		if p.Category != "" && e["category"] != p.Category || p.Source != "" && p.Source != "all" && e["source"] != p.Source {
			continue
		}
		head := foldCatalog(str(e["name"]) + " " + str(e["slug"]))
		tags := []string{}
		for _, t := range arr(e["tags"]) {
			tags = append(tags, str(t))
		}
		body := foldCatalog(str(e["summary"]) + " " + str(e["summaryEn"]) + " " + strings.Join(tags, " ") + " " + str(e["owner"]))
		match, headMatch := true, true
		for _, term := range terms {
			h := strings.Contains(head, term)
			match = match && (h || strings.Contains(body, term))
			headMatch = headMatch && h
		}
		if !match {
			continue
		}
		item := s.annotate(catalogSkill(e))
		if p.Security != "" && p.Security != "all" && item["securityStatus"] != p.Security {
			continue
		}
		if p.Installed == "installed" && item["installState"] != "installed" || p.Installed == "installable" && item["installState"] == "installed" {
			continue
		}
		if headMatch {
			strong = append(strong, item)
		} else {
			weak = append(weak, item)
		}
	}
	items := append(strong, weak...)
	offset := 0
	if strings.HasPrefix(p.Cursor, "catalog:") {
		offset = max(0, parseInt(strings.TrimPrefix(p.Cursor, "catalog:"), 0))
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 24
	}
	limit = min(100, limit)
	start := min(offset, len(items))
	end := min(start+limit, len(items))
	var next any
	if end < len(items) {
		next = "catalog:" + strconv.Itoa(end)
	}
	counts := map[string]int{}
	for _, v := range arr(catalog["skills"]) {
		counts[str(obj(v)["category"])]++
	}
	categories := []object{}
	for _, v := range arr(catalog["categories"]) {
		e := obj(v)
		count := counts[str(e["key"])]
		if count > 0 {
			category := clone(e)
			category["count"] = count
			categories = append(categories, category)
		}
	}
	status := object{"status": "ok", "fetchedAt": catalog["generatedAt"], "fromCache": true}
	return object{"scope": "catalog", "items": items[start:end], "nextCursor": next, "sources": object{"clawhub": status, "skillhub": clone(status)}, "total": len(items), "categories": categories, "catalogGeneratedAt": catalog["generatedAt"]}, nil
}
