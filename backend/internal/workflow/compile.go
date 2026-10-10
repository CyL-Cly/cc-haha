package workflow

import (
	"unicode/utf16"

	"github.com/dop251/goja"
)

// CompileBody compiles only user-authored workflow JavaScript. Metadata,
// permissions, orchestration and persistence are separate native producers.
// An executable grammar not covered by this engine stays an explicit cap.
func CompileBody(body []uint16) (*goja.Program, error) {
	for i := 0; i < len(body); i++ {
		if body[i] >= 0xd800 && body[i] <= 0xdbff {
			if i+1 >= len(body) || body[i+1] < 0xdc00 || body[i+1] > 0xdfff {
				return nil, &CapabilityError{"raw unpaired UTF-16 executable source"}
			}
			i++
		} else if body[i] >= 0xdc00 && body[i] <= 0xdfff {
			return nil, &CapabilityError{"raw unpaired UTF-16 executable source"}
		}
	}
	program, e := goja.Compile("workflow.js", "(async () => {\n"+string(utf16.Decode(body))+"\n})()", false)
	if e != nil {
		return nil, &CapabilityError{"native executable grammar: " + e.Error()}
	}
	return program, nil
}
