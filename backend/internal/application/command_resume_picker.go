package application

import (
	"context"
	"fmt"
	"io"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func commandTruthyJSON(raw []byte) bool { return openaiauth.AuthTruthy(raw) }
func selectCLIResumeFromPicker(ctx context.Context, c Command, o Options, in io.Reader, out io.Writer, env []string) (Command, bool, error) {
	picker, e := clibootstrap.NewCLIResumePicker(ctx, o.Native, o.Platform, c.Arguments.FromPR)
	if e != nil {
		return c, false, e
	}
	selected, e := tui.RunResumePicker(ctx, picker, tui.ResumePickerOptions{Input: in, Output: out, Environment: env})
	if e != nil {
		return c, false, e
	}
	if selected.Cancelled || !selected.Log.IsObject() {
		return c, false, nil
	}
	if selected.CrossProject {
		command := picker.CrossProjectCommand(selected.Log)
		_, e = fmt.Fprintf(out, "To resume this session, run:\n%s\n", command)
		return c, false, e
	}
	c.Arguments.Resume = selected.Log.String("fullPath")
	c.Arguments.ResumeWithoutValue = false
	// ResumeConversation's selected REPL receives existing messages, not the
	// initial-message or early-input props used by fresh interactive startup.
	c.Prompt = ""
	c.Arguments.Prefill = ""
	c.Arguments.ResumeAt = ""
	return c, true, nil
}
