package clibootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeBedrockInactiveOfflineHasNoCloudSideEffectsAndStillRejectsForeignScope(t *testing.T) {
	native := nativeForTest(t)
	args, err := ParsePrintArguments(mainRecipeArgs(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	main, err := NewMainPrint(MainPrintOptions{Arguments: args, Native: native, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: bytes.NewReader(nil), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer main.Close()
	if native.NativeProcess || main.awsCredentials != nil {
		t.Fatal("expected original offline fixture")
	}
	if err = main.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, flag := range []string{"", "0", "false", "off", "no"} {
			env := protocol.NewJSONObject(protocol.JSONField{Name: "CLAUDE_CODE_USE_BEDROCK", Value: quoteMain(flag)})
			if err := main.environment.Assign(env); err != nil {
				return err
			}
			binding, err := main.nativeBedrockBinding(scope)
			if err != nil || binding != nil {
				return errors.New("inactive provider incorrectly required cloud/native authority")
			}
		}
		env := protocol.NewJSONObject(protocol.JSONField{Name: "CLAUDE_CODE_USE_BEDROCK", Value: quoteMain("1")})
		if err := main.environment.Assign(env); err != nil {
			return err
		}
		if _, err := main.nativeBedrockBinding(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("unbound active Bedrock guard removed")
		}
		return main.environment.Assign(protocol.NewJSONObject(protocol.JSONField{Name: "CLAUDE_CODE_USE_BEDROCK", Value: quoteMain("0")}))
	}); err != nil {
		t.Fatal(err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err = foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := main.nativeBedrockBinding(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign inactive owner accepted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if main.awsCredentials != nil || main.awsSSOState != nil || main.awsINIState != nil {
		t.Fatal("inactive helper state allocated")
	}
}

func TestNativeVertexInactiveOfflineHasNoCloudSideEffectsAndStillRejectsForeignScope(t *testing.T) {
	native := nativeForTest(t)
	args, err := ParsePrintArguments(mainRecipeArgs(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	main, err := NewMainPrint(MainPrintOptions{Arguments: args, Native: native, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: bytes.NewReader(nil), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer main.Close()
	if native.NativeProcess || main.awsCredentials != nil {
		t.Fatal("expected original offline fixture")
	}
	if err = main.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, flag := range []string{"", "0", "false", "off", "no"} {
			env := protocol.NewJSONObject(protocol.JSONField{Name: "CLAUDE_CODE_USE_VERTEX", Value: quoteMain(flag)})
			if err := main.environment.Assign(env); err != nil {
				return err
			}
			binding, err := main.nativeVertexBinding(scope)
			if err != nil || binding != nil {
				return errors.New("inactive provider incorrectly required cloud/native authority")
			}
		}
		env := protocol.NewJSONObject(protocol.JSONField{Name: "CLAUDE_CODE_USE_VERTEX", Value: quoteMain("1")})
		if err := main.environment.Assign(env); err != nil {
			return err
		}
		if _, err := main.nativeVertexBinding(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("unbound active Vertex guard removed")
		}
		return main.environment.Assign(protocol.NewJSONObject(protocol.JSONField{Name: "CLAUDE_CODE_USE_VERTEX", Value: quoteMain("0")}))
	}); err != nil {
		t.Fatal(err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err = foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := main.nativeVertexBinding(scope); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign inactive owner accepted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if main.awsCredentials != nil || main.awsSSOState != nil || main.awsINIState != nil {
		t.Fatal("inactive helper state allocated")
	}
}
