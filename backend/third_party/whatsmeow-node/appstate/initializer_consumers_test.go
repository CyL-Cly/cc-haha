package appstate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"go.mau.fi/whatsmeow/proto/waServerSync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/util/cbcutil"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

type initializerKeyStore struct {
	store.NoopStore
	key *store.AppStateSyncKey
}

func (s *initializerKeyStore) GetAppStateSyncKey(context.Context, []byte) (*store.AppStateSyncKey, error) {
	return s.key, nil
}

func TestInitializedAppStateErrorsThroughRealMACAndHashValidation(t *testing.T) {
	ctx := context.Background()
	keysStore := &initializerKeyStore{}
	proc := NewProcessor(&store.Device{AppStateKeys: keysStore, AppState: &store.NoopStore{}}, waLog.Noop)
	keyID := []byte{7}
	state := HashState{Version: 8}
	name := WAPatchRegularLow
	for attempt := 0; attempt < 2; attempt++ {
		_, err := proc.getAppStateKey(ctx, keyID)
		if err != ErrKeyNotFound || err.Error() != "didn't find app state key" {
			t.Fatal("missing key sentinel", err)
		}
	}
	keysStore.key = &store.AppStateSyncKey{Data: bytes.Repeat([]byte{0x41}, 32)}
	keys, err := proc.getAppStateKey(ctx, keyID)
	if err != nil {
		t.Fatal(err)
	}
	snapshotMAC := state.generateSnapshotMAC(name, keys.SnapshotMAC)
	if _, err = proc.validateSnapshotMAC(ctx, name, state, keyID, snapshotMAC); err != nil {
		t.Fatal("valid snapshot", err)
	}
	badSnapshotMAC := bytes.Clone(snapshotMAC)
	badSnapshotMAC[0] ^= 1
	for attempt := 0; attempt < 2; attempt++ {
		_, err = proc.validateSnapshotMAC(ctx, name, state, keyID, badSnapshotMAC)
		if !errors.Is(err, ErrMismatchingLTHash) || err.Error() != "failed to verify patch v8: mismatching LTHash" {
			t.Fatal("hash sentinel", err)
		}
	}
	patch := &waServerSync.SyncdPatch{Version: &waServerSync.SyncdVersion{Version: proto.Uint64(8)}, KeyID: &waServerSync.KeyId{ID: keyID}, SnapshotMAC: snapshotMAC}
	patch.PatchMAC = generatePatchMAC(patch, name, keys.PatchMAC, 8)
	if _, _, err = proc.validatePatch(ctx, name, patch, state, true); err != nil {
		t.Fatal("valid patch", err)
	}
	patch.PatchMAC[0] ^= 1
	_, _, err = proc.validatePatch(ctx, name, patch, state, true)
	if !errors.Is(err, ErrMismatchingPatchMAC) || err.Error() != "failed to verify patch v8: mismatching patch MAC" {
		t.Fatal("patch sentinel", err)
	}

	index := []byte(`["contact","123@s.whatsapp.net"]`)
	action, err := proto.Marshal(&waSyncAction.SyncActionData{Index: index})
	if err != nil {
		t.Fatal(err)
	}
	iv := bytes.Repeat([]byte{0x22}, 16)
	ciphertext, err := cbcutil.Encrypt(keys.ValueEncryption, iv, action)
	if err != nil {
		t.Fatal(err)
	}
	content := append(bytes.Clone(iv), ciphertext...)
	valueMAC := generateContentMAC(waServerSync.SyncdMutation_SET, content, keyID, keys.ValueMAC)
	mutation := &waServerSync.SyncdMutation{Operation: waServerSync.SyncdMutation_SET.Enum(), Record: &waServerSync.SyncdRecord{
		KeyID: &waServerSync.KeyId{ID: keyID},
		Value: &waServerSync.SyncdValue{Blob: append(content, valueMAC...)},
		Index: &waServerSync.SyncdIndex{Blob: concatAndHMAC(sha256.New, keys.Index, index)},
	}}
	_, _, decoded, _, _, err := proc.decodeMutation(ctx, mutation, 0, true)
	if err != nil || len(decoded) != 2 || decoded[0] != "contact" {
		t.Fatal("valid encrypted mutation", decoded, err)
	}
	mutation.Record.Value.Blob[len(mutation.Record.Value.Blob)-1] ^= 1
	_, _, _, _, _, err = proc.decodeMutation(ctx, mutation, 0, true)
	if !errors.Is(err, ErrMismatchingContentMAC) || err.Error() != "failed to verify mutation #1: mismatching content MAC" {
		t.Fatal("content sentinel", err)
	}
	mutation.Record.Value.Blob[len(mutation.Record.Value.Blob)-1] ^= 1
	mutation.Record.Index.Blob[0] ^= 1
	_, _, _, _, _, err = proc.decodeMutation(ctx, mutation, 0, true)
	if !errors.Is(err, ErrMismatchingIndexMAC) || err.Error() != "failed to verify mutation #1: mismatching index MAC" {
		t.Fatal("index sentinel", err)
	}
	mutation.Operation = waServerSync.SyncdMutation_REMOVE.Enum()
	before := state
	warnings, err := state.updateHash([]*waServerSync.SyncdMutation{mutation}, func([]byte, int) ([]byte, error) { return nil, nil })
	if err != nil || len(warnings) != 1 || !errors.Is(warnings[0], ErrMissingPreviousSetValueOperation) || state != before {
		t.Fatal("missing previous SET warning or unchanged hash", warnings, state, err)
	}
	if ErrMismatchingLTHash == ErrMismatchingPatchMAC || ErrMismatchingContentMAC == ErrMismatchingIndexMAC || ErrKeyNotFound == ErrMissingPreviousSetValueOperation {
		t.Fatal("appstate sentinels became aliases")
	}
}
