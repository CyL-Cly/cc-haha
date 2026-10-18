package sqlstore_test

// These are offline storage-consumer regressions. The existing savedDevice
// helper creates only disposable synthetic device identities. A literal Client
// calls the existing BufferedDecrypt proxy without NewClient, Connect, sockets,
// account login, provider requests or real key/config reads.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
)

func bufferedStorageClient(device *store.Device) *whatsmeow.Client {
	return &whatsmeow.Client{Store: device, EnableDecryptedEventBuffer: true}
}

func bufferedStorageDevice(t *testing.T, container *sqlstore.Container, id types.JID) *store.Device {
	t.Helper()
	device, err := container.GetDevice(context.Background(), id)
	requireSQL(t, err)
	if device == nil || device.EventBuffer == nil || device.Sessions == nil {
		t.Fatal("cold-opened synthetic device did not bind its actual SQL stores")
	}
	return device
}

func bufferedStorageEvent(t *testing.T, device *store.Device, hash [32]byte) *store.BufferedEvent {
	t.Helper()
	event, err := device.EventBuffer.GetBufferedEvent(context.Background(), hash)
	requireSQL(t, err)
	return event
}

func assertBufferedStorageEvent(t *testing.T, device *store.Device, hash [32]byte, plaintext []byte, stamp time.Time) *store.BufferedEvent {
	t.Helper()
	event := bufferedStorageEvent(t, device, hash)
	if event == nil || !event.ServerTime.Equal(stamp) || event.InsertTime.IsZero() {
		t.Fatalf("durable buffered event/timestamps missing: %+v", event)
	}
	if plaintext == nil {
		if event.Plaintext != nil {
			t.Fatalf("processed hash retained plaintext: %q", event.Plaintext)
		}
	} else if !bytes.Equal(event.Plaintext, plaintext) {
		t.Fatalf("buffered plaintext: got=%q want=%q", event.Plaintext, plaintext)
	}
	return event
}

func TestSQLiteBufferedDecryptRollbackColdRetryCommitsRatchetAndEventTogether(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "buffered-rollback.db")
	container := openStore(t, path)
	device := savedDevice(t, container, "15550101")
	id := *device.ID
	address := "owned-peer:1"
	before := []byte("owned ratchet before failed decrypt")
	after := []byte("owned ratchet after successful decrypt")
	requireSQL(t, device.Sessions.PutSession(ctx, address, before))
	ciphertext := []byte("owned ciphertext whose first decrypt fails")
	hash := sha256.Sum256(ciphertext)
	auxiliary := sha256.Sum256([]byte("owned auxiliary transactional event"))
	stamp := time.Unix(1730001000, 0)
	failure := errors.New("owned decrypt callback failed after SQL writes")
	decryptCalls := 0
	client := bufferedStorageClient(device)
	_, _, err := client.DangerousInternals().BufferedDecrypt(ctx, ciphertext, stamp, func(tx context.Context) ([]byte, error) {
		decryptCalls++
		if err := device.Sessions.PutSession(tx, address, []byte("uncommitted ratchet")); err != nil {
			return nil, err
		}
		// A second real store call must share the callback's transaction, too.
		if err := device.EventBuffer.PutBufferedEvent(tx, auxiliary, []byte("uncommitted auxiliary plaintext"), stamp); err != nil {
			return nil, err
		}
		return nil, failure
	})
	if !errors.Is(err, failure) || decryptCalls != 1 {
		t.Fatalf("actual decrypt failure/callback count: error=%v calls=%d", err, decryptCalls)
	}
	assertSession(t, device, address, before)
	if bufferedStorageEvent(t, device, hash) != nil || bufferedStorageEvent(t, device, auxiliary) != nil {
		t.Fatal("failed decrypt committed an event or its transactional auxiliary write")
	}
	requireSQL(t, container.Close())
	container = openStore(t, path)
	device = bufferedStorageDevice(t, container, id)
	assertSession(t, device, address, before)
	if bufferedStorageEvent(t, device, hash) != nil || bufferedStorageEvent(t, device, auxiliary) != nil {
		t.Fatal("cold open exposed a rolled-back event")
	}
	client = bufferedStorageClient(device)
	want := []byte("owned plaintext after retry")
	plaintext, actualHash, err := client.DangerousInternals().BufferedDecrypt(ctx, ciphertext, stamp, func(tx context.Context) ([]byte, error) {
		decryptCalls++
		if err := device.Sessions.PutSession(tx, address, after); err != nil {
			return nil, err
		}
		return want, nil
	})
	requireSQL(t, err)
	if decryptCalls != 2 || actualHash != hash || !bytes.Equal(plaintext, want) {
		t.Fatalf("rolled-back ciphertext was not retryable: calls=%d hash=%x plaintext=%q", decryptCalls, actualHash, plaintext)
	}
	assertSession(t, device, address, after)
	committed := assertBufferedStorageEvent(t, device, hash, want, stamp)
	requireSQL(t, container.Close())
	container = openStore(t, path)
	device = bufferedStorageDevice(t, container, id)
	assertSession(t, device, address, after)
	cold := assertBufferedStorageEvent(t, device, hash, want, stamp)
	if !cold.InsertTime.Equal(committed.InsertTime) || bufferedStorageEvent(t, device, auxiliary) != nil {
		t.Fatal("cold open changed the committed insertion identity or resurrected a rolled-back row")
	}
}

func TestSQLiteBufferedDecryptColdCacheAndClearKeepProcessedHashDurable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "buffered-dedup.db")
	container := openStore(t, path)
	device := savedDevice(t, container, "15550102")
	id := *device.ID
	address := "owned-dedup-peer:1"
	ciphertext := []byte("owned ciphertext delivered repeatedly")
	hash := sha256.Sum256(ciphertext)
	stamp := time.Unix(1730002000, 0)
	want := []byte("owned plaintext awaiting successful handling")
	ratchet := []byte("owned committed ratchet must not advance again")
	decryptCalls := 0
	client := bufferedStorageClient(device)
	plaintext, actualHash, err := client.DangerousInternals().BufferedDecrypt(ctx, ciphertext, stamp, func(tx context.Context) ([]byte, error) {
		decryptCalls++
		if err := device.Sessions.PutSession(tx, address, ratchet); err != nil {
			return nil, err
		}
		return want, nil
	})
	requireSQL(t, err)
	if decryptCalls != 1 || actualHash != hash || !bytes.Equal(plaintext, want) {
		t.Fatal("initial actual buffered decrypt did not commit its known plaintext")
	}
	committed := assertBufferedStorageEvent(t, device, hash, want, stamp)
	requireSQL(t, container.Close())
	container = openStore(t, path)
	device = bufferedStorageDevice(t, container, id)
	client = bufferedStorageClient(device)
	unexpectedCalls := 0
	shouldNotDecrypt := func(context.Context) ([]byte, error) {
		unexpectedCalls++
		return nil, errors.New("duplicate ciphertext invoked decryption again")
	}
	// A fresh Client and container must reuse disk plaintext, not process memory.
	plaintext, actualHash, err = client.DangerousInternals().BufferedDecrypt(ctx, ciphertext, stamp.Add(time.Hour), shouldNotDecrypt)
	requireSQL(t, err)
	if unexpectedCalls != 0 || actualHash != hash || !bytes.Equal(plaintext, want) {
		t.Fatal("cold retry reran decryption instead of reading the committed buffer")
	}
	cached := assertBufferedStorageEvent(t, device, hash, want, stamp)
	if !cached.InsertTime.Equal(committed.InsertTime) {
		t.Fatal("cached retry replaced insertion time")
	}
	requireSQL(t, device.EventBuffer.ClearBufferedEventPlaintext(ctx, hash))
	requireSQL(t, device.EventBuffer.ClearBufferedEventPlaintext(ctx, hash))
	cleared := assertBufferedStorageEvent(t, device, hash, nil, stamp)
	if !cleared.InsertTime.Equal(committed.InsertTime) {
		t.Fatal("clearing plaintext changed the retained replay identity")
	}
	missingHash := sha256.Sum256([]byte("owned event never inserted"))
	requireSQL(t, device.EventBuffer.ClearBufferedEventPlaintext(ctx, missingHash))
	if bufferedStorageEvent(t, device, missingHash) != nil {
		t.Fatal("clearing a missing hash inserted a phantom event")
	}
	requireSQL(t, container.Close())
	container = openStore(t, path)
	device = bufferedStorageDevice(t, container, id)
	cold := assertBufferedStorageEvent(t, device, hash, nil, stamp)
	if !cold.InsertTime.Equal(committed.InsertTime) {
		t.Fatal("cold open lost the processed hash insertion time")
	}
	client = bufferedStorageClient(device)
	plaintext, actualHash, err = client.DangerousInternals().BufferedDecrypt(ctx, ciphertext, stamp.Add(2*time.Hour), shouldNotDecrypt)
	if !errors.Is(err, whatsmeow.EventAlreadyProcessed) || unexpectedCalls != 0 || actualHash != hash || plaintext != nil {
		t.Fatalf("processed cold replay: error=%v calls=%d hash=%x plaintext=%q", err, unexpectedCalls, actualHash, plaintext)
	}
	assertSession(t, device, address, ratchet)
}

func TestSQLiteDecryptionConstraintRollbackAndClearAreDeviceScoped(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "buffered-device-scope.db")
	container := openStore(t, path)
	a := savedDevice(t, container, "15550103")
	b := savedDevice(t, container, "15550104")
	aid, bid := *a.ID, *b.ID
	address := "owned-scoped-peer:1"
	before := []byte("device A ratchet before constraint failure")
	sibling := []byte("device B ratchet remains independent")
	requireSQL(t, a.Sessions.PutSession(ctx, address, before))
	requireSQL(t, b.Sessions.PutSession(ctx, address, sibling))
	hash := sha256.Sum256([]byte("same owned ciphertext hash on two devices"))
	stampA, stampB := time.Unix(1730003000, 0), time.Unix(1730004000, 0)
	plainA, plainB := []byte("device A plaintext"), []byte("device B plaintext")
	requireSQL(t, a.EventBuffer.PutBufferedEvent(ctx, hash, plainA, stampA))
	requireSQL(t, b.EventBuffer.PutBufferedEvent(ctx, hash, plainB, stampB))
	storedA := assertBufferedStorageEvent(t, a, hash, plainA, stampA)
	storedB := assertBufferedStorageEvent(t, b, hash, plainB, stampB)
	var duplicateErr error
	err := a.EventBuffer.DoDecryptionTxn(ctx, func(tx context.Context) error {
		if err := a.Sessions.PutSession(tx, address, []byte("must roll back on duplicate insert")); err != nil {
			return err
		}
		// This is a real SQLite uniqueness failure, not a mocked callback error.
		duplicateErr = a.EventBuffer.PutBufferedEvent(tx, hash, []byte("must not replace device A event"), stampA.Add(time.Hour))
		return duplicateErr
	})
	if duplicateErr == nil || !errors.Is(err, duplicateErr) {
		t.Fatalf("actual duplicate insert did not abort its enclosing transaction: duplicate=%v transaction=%v", duplicateErr, err)
	}
	assertSession(t, a, address, before)
	assertSession(t, b, address, sibling)
	afterA := assertBufferedStorageEvent(t, a, hash, plainA, stampA)
	if !afterA.InsertTime.Equal(storedA.InsertTime) {
		t.Fatal("constraint failure replaced the original buffered event")
	}
	requireSQL(t, container.Close())
	container = openStore(t, path)
	a = bufferedStorageDevice(t, container, aid)
	b = bufferedStorageDevice(t, container, bid)
	assertSession(t, a, address, before)
	assertSession(t, b, address, sibling)
	assertBufferedStorageEvent(t, a, hash, plainA, stampA)
	assertBufferedStorageEvent(t, b, hash, plainB, stampB)
	requireSQL(t, a.EventBuffer.ClearBufferedEventPlaintext(ctx, hash))
	assertBufferedStorageEvent(t, a, hash, nil, stampA)
	stillB := assertBufferedStorageEvent(t, b, hash, plainB, stampB)
	if !stillB.InsertTime.Equal(storedB.InsertTime) {
		t.Fatal("clearing device A changed device B replay identity")
	}
	requireSQL(t, container.Close())
	container = openStore(t, path)
	a = bufferedStorageDevice(t, container, aid)
	b = bufferedStorageDevice(t, container, bid)
	coldA := assertBufferedStorageEvent(t, a, hash, nil, stampA)
	coldB := assertBufferedStorageEvent(t, b, hash, plainB, stampB)
	if !coldA.InsertTime.Equal(storedA.InsertTime) || !coldB.InsertTime.Equal(storedB.InsertTime) {
		t.Fatal("cold open changed device-scoped retained event identities")
	}
}
