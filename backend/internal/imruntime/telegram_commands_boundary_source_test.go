package imruntime

import (
	"os"
	"path/filepath"
	"testing"
)

type telegramBoundaryActions struct {
	rows              []map[string]any
	message, callback int
}

func (a *telegramBoundaryActions) user(text string) map[string]any {
	a.message++
	name := text
	for i, r := range text {
		if r == ' ' {
			name = text[:i]
			break
		}
	}
	row := map[string]any{"update": map[string]any{"update_id": a.message, "message": map[string]any{"message_id": a.message, "date": 1, "from": map[string]any{"id": 42, "first_name": "Fixture", "is_bot": false}, "chat": map[string]any{"id": 42, "type": "private"}, "text": text, "entities": []any{map[string]any{"offset": 0, "length": len(NewText(name)), "type": "bot_command"}}}}}
	a.rows = append(a.rows, row)
	return row
}
func (a *telegramBoundaryActions) pick(data string, user int) map[string]any {
	a.callback++
	row := map[string]any{"update": map[string]any{"update_id": 100 + a.callback, "callback_query": map[string]any{"id": "boundary-" + stringNumber(a.callback), "from": map[string]any{"id": user, "first_name": "Fixture", "is_bot": false}, "message": map[string]any{"message_id": 99, "date": 1, "text": "original", "chat": map[string]any{"id": 42, "type": "private"}}, "data": data}}}
	a.rows = append(a.rows, row)
	return row
}
func telegramBoundaryWorkspace(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	work := filepath.Join(home, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	return home, existingDir(work, home)
}
func TestActualTelegramCommandHTTPFailuresEmptyCatalogsAndSelectionBoundaries(t *testing.T) {
	const now int64 = 1791404000000
	t.Run("http-failures", func(t *testing.T) {
		home, work := telegramBoundaryWorkspace(t)
		a := telegramBoundaryActions{}
		failure := func(path string) map[string]telegramEntryResponse {
			return map[string]telegramEntryResponse{path: {Status: 503, Body: map[string]any{"error": "fixture service temporarily unavailable"}}}
		}
		a.user("/provider")["responses"] = failure("GET /api/providers")
		a.user("/provider official")["responses"] = failure("POST /api/providers/official")
		a.user("/provider private-provider")["responses"] = failure("POST /api/providers/private-provider/activate")
		a.user("/model failed-model")["responses"] = failure("PUT /api/models/current")
		a.user("/model")["responses"] = failure("GET /api/models")
		a.user("/resume")["responses"] = failure("GET /api/sessions/recent-projects")
		a.user("/skills")["responses"] = failure("GET /api/skills?cwd=" + EncodeURIComponent(work))
		runTelegramEntrypointScenario(t, home, work, now, []any{}, []any{}, a.rows, nil)
	})
	t.Run("error-message-shapes-and-status-fallback", func(t *testing.T) {
		home, work := telegramBoundaryWorkspace(t)
		a := telegramBoundaryActions{}
		for _, value := range []any{nil, false, 27, map[string]any{"future": true}, []any{"part", nil, map[string]any{"future": 1}}} {
			a.user("/provider")["responses"] = map[string]telegramEntryResponse{"GET /api/providers": {Status: 503, Body: map[string]any{"message": value}}}
		}
		malformed := "upstream maintenance HTML"
		a.user("/provider")["responses"] = map[string]telegramEntryResponse{"GET /api/providers": {Status: 503, Raw: &malformed}}
		a.user("/provider")["responses"] = map[string]telegramEntryResponse{"GET /api/providers": {Status: 503, Body: []any{1, 2}}}
		a.user("/provider")["responses"] = map[string]telegramEntryResponse{"GET /api/providers": {Status: 503, Body: nil}}
		a.user("/model no-json-needed")["responses"] = map[string]telegramEntryResponse{"PUT /api/models/current": {Status: 200, Raw: &malformed}}
		runTelegramEntrypointScenario(t, home, work, now, []any{}, []any{}, a.rows, nil)
	})

	t.Run("empty-catalogs", func(t *testing.T) {
		home, work := telegramBoundaryWorkspace(t)
		a := telegramBoundaryActions{}
		a.user("/model")["responses"] = map[string]telegramEntryResponse{"GET /api/models": {Status: 200, Body: map[string]any{"models": []any{}}}}
		a.user("/skills")["responses"] = map[string]telegramEntryResponse{"GET /api/skills?cwd=" + EncodeURIComponent(work): {Status: 200, Body: map[string]any{"skills": []any{}}}}
		a.user("/resume")["responses"] = map[string]telegramEntryResponse{}
		runTelegramEntrypointScenario(t, home, work, now, []any{}, []any{}, a.rows, nil)
	})
	t.Run("pages-noop-invalid-expired-dedup", func(t *testing.T) {
		home, work := telegramBoundaryWorkspace(t)
		a := telegramBoundaryActions{}
		providers := []any{}
		for i := 0; i < 16; i++ {
			providers = append(providers, map[string]any{"id": "provider-" + stringNumber(i), "name": "Provider " + stringNumber(i), "models": map[string]any{"main": "fixture-model"}})
		}
		a.user("/provider")["responses"] = map[string]telegramEntryResponse{"GET /api/providers": {Status: 200, Body: map[string]any{"providers": providers, "activeId": "provider-7"}}}
		a.pick("tgsel:provider:page:1", 42)
		a.pick("tgsel:provider:noop:0", 42)
		a.pick("tgsel:provider:pick:999", 42)
		a.pick("tgsel:model:pick:0", 42)
		a.pick("tgsel:provider:pick:0", 43)
		a.pick("tgsel:provider:page:99", 42)
		a.pick("tgsel:provider:pick:0", 42)["now"] = now + 900001
		duplicate := a.pick("tgsel:provider:pick:0", 42)
		a.rows = append(a.rows, duplicate)
		runTelegramEntrypointScenario(t, home, work, now, []any{}, []any{}, a.rows, nil)
	})
}
