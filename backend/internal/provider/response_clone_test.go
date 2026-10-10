package provider

import (
	"bytes"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchCloneReadsUnreadTailAfterSDKBranchCloses(t *testing.T) {
	var closes atomic.Int32
	sdk, clone := forkResponse(bytes.NewBufferString("first second tail"), func() error { closes.Add(1); return nil })
	prefix := make([]byte, 5)
	if n, e := sdk.Read(prefix); n != 5 || e != nil || string(prefix) != "first" {
		t.Fatal(n, e, string(prefix))
	}
	if e := sdk.Close(); e != nil || closes.Load() != 0 {
		t.Fatal("SDK branch prematurely closed physical fetch", e, closes.Load())
	}
	actual, e := io.ReadAll(clone)
	if e != nil || string(actual) != "first second tail" {
		t.Fatal("clone lost unconsumed body", string(actual), e)
	}
	if e = clone.Close(); e != nil || closes.Load() != 1 {
		t.Fatal("physical fetch not closed exactly once", e, closes.Load())
	}
	_ = sdk.Close()
	_ = clone.Close()
	if closes.Load() != 1 {
		t.Fatal("duplicate closure")
	}
}

func TestFetchCloneParallelConsumersRetainIdenticalBytesWithDifferentReadSizes(t *testing.T) {
	input := bytes.Repeat([]byte("😀\nactual independent streaming bytes\n"), 20000)
	var closes atomic.Int32
	sdk, clone := forkResponse(bytes.NewReader(input), func() error { closes.Add(1); return nil })
	type outcome struct {
		bytes []byte
		err   error
	}
	results := make(chan outcome, 2)
	for i, branch := range []*responseBranch{sdk, clone} {
		go func(size int, r io.ReadCloser) {
			var result bytes.Buffer
			buffer := make([]byte, size)
			for {
				n, e := r.Read(buffer)
				result.Write(buffer[:n])
				if e != nil {
					if e == io.EOF {
						e = nil
					}
					_ = r.Close()
					results <- outcome{result.Bytes(), e}
					return
				}
			}
		}(127+i*8065, branch)
	}
	for i := 0; i < 2; i++ {
		select {
		case result := <-results:
			if result.err != nil || !bytes.Equal(result.bytes, input) {
				t.Fatal("concurrent clone corrupted body", len(result.bytes), result.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("clone consumer leaked")
		}
	}
	if closes.Load() != 1 {
		t.Fatal("physical body leak", closes.Load())
	}
}

func TestBothFetchCloneBranchesClosingUnblocksHeldPhysicalRead(t *testing.T) {
	reader, writer := io.Pipe()
	sdk, clone := forkResponse(reader, reader.Close)
	read := make(chan error, 1)
	go func() { _, e := clone.Read(make([]byte, 64)); read <- e }()
	if e := sdk.Close(); e != nil {
		t.Fatal(e)
	}
	closed := make(chan error, 1)
	go func() { closed <- clone.Close() }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("closing both branches did not join native reader")
	}
	select {
	case e := <-read:
		if e == nil {
			t.Fatal("closed read returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("physical reader leaked")
	}
	_ = writer.Close()
}

// The trace branch may demand another network chunk while the model branch
// is still waiting for the previous one. Its demand must not hold queued bytes.
func TestFetchCloneQueuedPeerBytesDoNotWaitForTheNextPhysicalChunk(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	firstRead := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondRead := make(chan struct{})
	releaseSecond := make(chan struct{})
	var firstRelease, secondRelease sync.Once
	var reads atomic.Int32
	source := cloneHeldReader(func(out []byte) (int, error) {
		switch reads.Add(1) {
		case 1:
			close(firstRead)
			<-releaseFirst
			return copy(out, "queued-model-data"), nil
		case 2:
			close(secondRead)
			<-releaseSecond
			return 0, io.EOF
		default:
			return 0, io.EOF
		}
	})
	sdk, trace := forkResponse(source, func() error {
		firstRelease.Do(func() { close(releaseFirst) })
		secondRelease.Do(func() { close(releaseSecond) })
		return nil
	})
	defer func() { _ = sdk.Close(); _ = trace.Close() }()
	traceDone := make(chan struct{})
	go func() {
		defer close(traceDone)
		buffer := make([]byte, 64)
		_, _ = trace.Read(buffer)
		_, _ = trace.Read(buffer)
	}()
	<-firstRead
	type result struct {
		data string
		err  error
	}
	sdkDone := make(chan result, 1)
	go func() {
		buffer := make([]byte, 64)
		n, err := sdk.Read(buffer)
		sdkDone <- result{string(buffer[:n]), err}
	}()
	// Yield the one runnable processor to the peer before delivering chunk one.
	runtime.Gosched()
	firstRelease.Do(func() { close(releaseFirst) })
	<-secondRead
	select {
	case got := <-sdkDone:
		if got.data != "queued-model-data" || got.err != nil {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("model could not consume already queued bytes while trace awaited the next physical chunk")
	}
	secondRelease.Do(func() { close(releaseSecond) })
	<-traceDone
}

type cloneHeldReader func([]byte) (int, error)

func (read cloneHeldReader) Read(out []byte) (int, error) { return read(out) }
