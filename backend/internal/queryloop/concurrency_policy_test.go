package queryloop

import (
	"context"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestSourceToolUseConcurrencyRetainsDecimalPrefixAndFalsyDefault(t *testing.T) {
	for _, scenario := range []struct {
		input string
		want  int
	}{{"", 10}, {"invalid", 10}, {"Infinity", 10}, {"0", 10}, {"-0", 10}, {"0x20", 10}, {"2.9", 2}, {"3e4", 3}, {"\ufeff\u202f+4 tail", 4}, {"\u00852", 10}, {"-2", -2}, {strings.Repeat("9", 400), int(^uint(0) >> 1)}} {
		if got := SourceToolUseConcurrency(scenario.input); got != scenario.want {
			t.Fatalf("Source parseInt(%q)||10 = %d, want %d", scenario.input, got, scenario.want)
		}
	}
}

func TestNegativeSourceConcurrencySkipsReadonlyPartitionAndContinuesSerialTool(t *testing.T) {
	h := newBatchHarness(t)
	read := h.Tool
	read.Name = "Read"
	read.IsConcurrencySafe = func(protocol.JSONObject) bool { return true }
	owner := cooperative.New()
	defer owner.Close()
	use := h.Uses[0]
	block := *use.Block
	block.Object = block.Object.Clone()
	block.Object.Set("name", quoted("Read"))
	use.Block = &block
	uses := append([]Invocation{use}, h.Uses...)
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		h.Capabilities.Owner, h.Capabilities.Scope, h.Capabilities.MaxConcurrency = owner, scope, -2
		batch, err := NewToolBatch([]RegisteredTool{read, h.Tool}, h.Capabilities, h.Factory)
		if err != nil {
			return err
		}
		_, err = batch.Execute(context.Background(), Round{ToolContext: "original"}, uses, h.Emit)
		return err
	})
	if err != nil || h.Calls != 1 || h.UUIDs != 1 {
		t.Fatalf("readonly partition did not retain Source negative-cap skip: error=%v calls=%d UUIDs=%d", err, h.Calls, h.UUIDs)
	}
}
