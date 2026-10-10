package application

import "context"

func (s *Session) WorkflowKeywordEnabled(ctx context.Context) (bool, error) {
	return s.process.NativeWorkflowKeywordEnabled(ctx)
}
