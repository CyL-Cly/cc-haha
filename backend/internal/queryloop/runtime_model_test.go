package queryloop

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func TestCurrentModelAdmissionIsPerRoundAndReachesActualRequest(t *testing.T) {
	models := []string{}
	h := testHarness(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Model string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		models = append(models, body.Model)
		w.Header().Set("Content-Type", "text/event-stream")
		wire := completeText()
		if len(models) == 1 {
			wire = completeTool()
		}
		_, _ = io.WriteString(w, wire)
	})
	h.Host.Preflight = nil
	current := ""
	h.Host.ResolvePreflight = func(_ context.Context, round Round) (PreflightResult, error) {
		current = "deepseek-flash"
		if round.Number == 2 {
			current = "fixture-model"
		}
		return PreflightResult{History: round.History, ToolContext: round.ToolContext, CurrentModel: current}, nil
	}
	resolve := h.Host.ResolveModelInput
	h.Host.ResolveModelInput = func(ctx context.Context, round Round) (requestretry.Input, error) {
		in, err := resolve(ctx, round)
		in.Context.Options.Model = current
		return in, err
	}
	out, err := h.run(context.Background())
	if err != nil || out.Reason != "completed" || !reflect.DeepEqual(models, []string{"deepseek-flash", "fixture-model"}) {
		t.Fatalf("actual per-round model admission/request: %+v %v %v", out, models, err)
	}
}

func TestCurrentModelAdmissionDoesNotAcceptUnselectedOrForeignInput(t *testing.T) {
	for _, mutation := range []string{"missing-selection", "wrong-model", "query-source", "interactive"} {
		t.Run(mutation, func(t *testing.T) {
			h := testHarness(t, wireHandler(completeText()))
			h.Host.Preflight = nil
			h.Host.ResolvePreflight = func(_ context.Context, round Round) (PreflightResult, error) {
				model := "deepseek-flash"
				if mutation == "missing-selection" {
					model = ""
				}
				return PreflightResult{History: round.History, CurrentModel: model}, nil
			}
			resolve := h.Host.ResolveModelInput
			h.Host.ResolveModelInput = func(ctx context.Context, round Round) (requestretry.Input, error) {
				in, err := resolve(ctx, round)
				in.Context.Options.Model = "deepseek-flash"
				switch mutation {
				case "wrong-model":
					in.Context.Options.Model = "fixture-model"
				case "query-source":
					in.Context.Options.QuerySource = "foreign"
				case "interactive":
					in.Context.Options.IsNonInteractiveSession = false
				}
				return in, err
			}
			_, err := h.run(context.Background())
			scope(t, err, "original model input/outer options mismatch")
			if h.Requests.Load() != 0 {
				t.Fatal("foreign model reached provider")
			}
		})
	}
}
