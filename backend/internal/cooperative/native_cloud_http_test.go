package cooperative

import (
	"context"
	"errors"
	"testing"
)

func TestNativeCloudHTTPRejectsUnboundReadForeignOwnerAndWrongPurpose(t *testing.T) {
	owner := New()
	defer owner.Close()
	foreign := New()
	defer foreign.Close()
	var proof *NativeCloudHTTP
	var stale *Scope
	if err := owner.Run(context.Background(), func(scope *Scope) error {
		stale = scope
		if _, err := NewNativeCloudHTTP(nil, owner, "aws_credentials"); !errors.Is(err, ErrRestricted) {
			return errors.New("nil scope minted cloud purpose")
		}
		if _, err := NewNativeCloudHTTP(scope, foreign, "aws_credentials"); !errors.Is(err, ErrRestricted) {
			return errors.New("foreign owner minted cloud purpose")
		}
		if _, err := NewNativeCloudHTTP(scope, owner, "image_generate"); !errors.Is(err, ErrRestricted) {
			return errors.New("noncloud purpose minted")
		}
		var err error
		proof, err = NewNativeCloudHTTP(scope, owner, "aws_credentials")
		if err != nil {
			return err
		}
		return scope.Inline(Read, func(callback *Scope) error {
			if _, err := NewNativeCloudHTTP(callback, owner, "aws_credentials"); !errors.Is(err, ErrRestricted) {
				return errors.New("ordinary Read minted cloud HTTP")
			}
			if err := proof.Run(callback, context.Background(), "gcp_credentials", func(*Scope) error { return errors.New("wrong purpose ran") }); !errors.Is(err, ErrRestricted) {
				return errors.New("AWS proof admitted GCP producer")
			}
			if err := proof.Run(nil, context.Background(), "aws_credentials", func(*Scope) error { return nil }); !errors.Is(err, ErrRestricted) {
				return errors.New("nil caller ran")
			}
			return proof.Run(callback, context.Background(), "aws_credentials", func(cloud *Scope) error {
				if cloud.callback != 0 || cloud.owner != owner {
					return errors.New("cloud continued with wrong owner/callback")
				}
				return nil
			})
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := proof.Run(stale, context.Background(), "aws_credentials", func(*Scope) error { return nil }); !errors.Is(err, ErrRestricted) {
		t.Fatal("stale scope ran", err)
	}
	if err := foreign.Run(context.Background(), func(scope *Scope) error {
		if err := proof.Run(scope, context.Background(), "aws_credentials", func(*Scope) error { return nil }); !errors.Is(err, ErrRestricted) {
			return errors.New("foreign caller ran")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A valid cloud cache continuation may resume while the original ASK is
	// idle, but that Read callback still cannot mint a new cloud capability.
	receipt, err := owner.SubmitIndependent(context.Background(), Read, func(scope *Scope) error {
		return proof.Run(scope, context.Background(), "aws_credentials", func(cloud *Scope) error {
			_, err := cloud.AwaitBytes(NetworkOpen, func(context.Context) ([]byte, error) { return []byte("original credential HTTP bytes"), nil })
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = receipt.Wait(); err != nil {
		t.Fatal(err)
	}
}
