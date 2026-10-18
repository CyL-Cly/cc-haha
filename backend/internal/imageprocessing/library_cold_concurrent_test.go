//go:build darwin || linux

package imageprocessing

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestActualBundledCodecColdConcurrentInitializationAndReadInFreshProcess(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("packaged fixture library is darwin arm64")
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		cmd := exec.Command(executable, "-test.run=^TestBundledCodecColdConcurrentProcessProbe$", "-test.v")
		cmd.Env = append(os.Environ(), "CC_HAHA_BUNDLED_CODEC_COLD_PROBE=1")
		capturePublicGoCoverage(t, cmd)
		p, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("actual cold process%d %v\n%s", i, e, p)
		}
		if !bytes.Contains(p, []byte("COLD_SHARED_CODEC_32_READS_OK")) {
			t.Fatal("fresh subprocess did not execute cold native codec")
		}
	}
}
func TestBundledCodecColdConcurrentProcessProbe(t *testing.T) {
	if os.Getenv("CC_HAHA_BUNDLED_CODEC_COLD_PROBE") != "1" {
		return
	}
	library, e := filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	if e != nil {
		t.Fatal(e)
	}
	var encoded bytes.Buffer
	if e = png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 12, 8))); e != nil {
		t.Fatal(e)
	}
	data := encoded.Bytes()
	start := make(chan struct{})
	results := make(chan Codec, 32)
	failures := make(chan error, 32)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			codec, e := BundledCodec(library)
			if e != nil {
				failures <- e
				return
			}
			results <- codec
			read, e := ReadImage(codec, data, "private-cold-image.png", 2000)
			if e != nil {
				failures <- e
				return
			}
			if read.MediaType != "png" || !bytes.Equal(read.Buffer, data) {
				failures <- fmt.Errorf("cold read normalized bytes changed")
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		t.Fatal(e)
	}
	var first Codec
	count := 0
	for codec := range results {
		if first == nil {
			first = codec
		} else if first != codec {
			t.Fatal("concurrent callers received separate cold codec instances")
		}
		count++
	}
	if count != 32 {
		t.Fatal("cold native reads not all completed", count)
	}
	t.Log("COLD_SHARED_CODEC_32_READS_OK")
}
