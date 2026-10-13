package queryloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelyield"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// A model fallback restarts this same sampling iteration, rather than entering
// another tool round or rerunning compaction/hooks. consume owns and releases
// the failed attempt's accounting and transport before the warning is yielded.
func consumeWithModelFallback(ctx context.Context, input requestretry.Input, options Options, host Host, emit Emit, stopped func() bool) ([]*streamhistory.Envelope, []Invocation, modelyield.Outcome, error) {
	input.FallbackModel = options.FallbackModel
	for {
		assistants, uses, outcome, err := consume(ctx, input, options, host, emit, stopped)
		var fallback *retrypolicy.FallbackTriggeredError
		if options.FallbackModel == "" || !errors.As(err, &fallback) {
			return assistants, uses, outcome, err
		}
		if host.ModelFallbackChanged != nil {
			if err := host.ModelFallbackChanged(ctx, options.FallbackModel); err != nil {
				return nil, nil, modelyield.Outcome{}, err
			}
		}
		input.Context.Options.Model = options.FallbackModel
		if input.Context.Host.ToolSchemaHost != nil {
			copy := *input.Context.Host.ToolSchemaHost
			copy.MainLoopModel = options.FallbackModel
			input.Context.Host.ToolSchemaHost = &copy
		}
		originalName, fallbackName := fallback.OriginalModel, fallback.FallbackModel
		if host.ModelDisplayName != nil {
			var err error
			originalName, err = host.ModelDisplayName(originalName)
			if err != nil {
				return nil, nil, modelyield.Outcome{}, err
			}
			fallbackName, err = host.ModelDisplayName(fallbackName)
			if err != nil {
				return nil, nil, modelyield.Outcome{}, err
			}
		}
		factory, err := messages.NewFactory(options.UUID, options.Now)
		if err != nil {
			return nil, nil, modelyield.Outcome{}, err
		}
		content, err := json.Marshal(fmt.Sprintf("Switched to %s due to high demand for %s", fallbackName, originalName))
		if err != nil {
			return nil, nil, modelyield.Outcome{}, err
		}
		warning := Message{Record: factory.CreateSystemInformation(content, "warning")}
		if err := emit(Yield{Message: &warning, ModelFallbackWarning: true}); err != nil {
			return nil, nil, modelyield.Outcome{}, err
		}
		// The next consume creates fresh assistant/tool collections. A failed
		// model creation has not admitted a tool invocation to ExecuteBatch.
	}
}
