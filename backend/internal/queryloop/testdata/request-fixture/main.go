// Integration fixture entry. The JSON input cannot supply prepared requests.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop/testdata/request-fixture/driver"
)

func main() {
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	var input driver.Input
	if err := decoder.Decode(&input); err != nil {
		panic(err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		panic("single original-input fixture required")
	}
	if err := driver.Guard(input); err != nil {
		panic(err)
	}
	observation, err := driver.Observe(input)
	if err != nil {
		panic(err)
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		panic(err)
	}
	output, err := json.Marshal(map[string]any{"observationSource": string(raw)})
	if err != nil {
		panic(err)
	}
	fmt.Print(string(output))
}
