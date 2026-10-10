package sdkhost

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"reflect"
	"testing"
)

func TestStructuredPrependEmptyStreamAndBetweenSameBlockMessages(t *testing.T) {
	for _, chunks := range [][]string{nil, {`{"type":"user","message":{"role":"user","content":"first"}}` + "\n" + `{"type":"user","message":{"role":"user","content":"second"}}` + "\n"}} {
		owner := cooperative.New()
		err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			raw, _ := chunkIterator(owner, chunks)
			input, _ := NewPlainStructuredInput(owner, raw)
			if err := input.PrependUserMessage(scope, json.RawMessage(`"initial\ud800"`)); err != nil {
				return err
			}
			seen := []string{}
			for {
				future, e := input.Next(scope)
				if e != nil {
					return e
				}
				result, e := future.Await(scope)
				if e != nil {
					return e
				}
				if result.Done {
					break
				}
				api, _ := result.Value.Object("message")
				seen = append(seen, string(api.Get("content")))
				if api.String("content") == "first" {
					if e = input.PrependUserMessage(scope, json.RawMessage(`"between"`)); e != nil {
						return e
					}
				}
			}
			want := []string{`"initial\ud800"`}
			if len(chunks) > 0 {
				want = append(want, `"first"`, `"between"`, `"second"`)
			}
			if !reflect.DeepEqual(seen, want) {
				t.Errorf("same source input queue %v/%v", seen, want)
			}
			return nil
		})
		owner.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
