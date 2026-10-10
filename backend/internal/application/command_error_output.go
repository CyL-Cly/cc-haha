package application

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// Source's console.error terminates a line, while these writeToStderr
// validations write exactly their message. Keep that distinction at the real
// CLI consumer without altering the API's ordinary error text.
type commandValidationError struct{ output string }

func (err *commandValidationError) Error() string { return strings.TrimSuffix(err.output, "\n") }

func WriteCommandError(writer io.Writer, err error) error {
	var validation *commandValidationError
	if errors.As(err, &validation) {
		_, e := io.WriteString(writer, validation.output)
		return e
	}
	_, e := fmt.Fprintln(writer, err)
	return e
}
