package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func (s *Session) initialCLITerminalTitle(ctx context.Context) (string, error) {
	var title string
	receipt, err := s.process.Owner.SubmitIndependent(ctx, cooperative.Read, func(*cooperative.Scope) error {
		metadata, _ := s.process.Controller.Writer().Snapshot().Object("metadata")
		title = metadata.String("customTitle")
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return title, err
}
