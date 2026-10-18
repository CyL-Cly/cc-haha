//go:build darwin

package textsanitization

import (
	"fmt"
	"math"
	"runtime"
	"sync"
	"unicode/utf16"

	"github.com/ebitengine/purego"
)

type nativeICUNormalization struct {
	instance  uintptr
	normalize func(uintptr, *uint16, int32, *uint16, int32, *int32) int32
}

var nativeICUNormalizer = sync.OnceValues(func() (*nativeICUNormalization, error) {
	// The pinned macOS Bun executable imports unorm2_* from this same dylib.
	// Its String.normalize data is independent of its regex category data.
	library, err := purego.Dlopen("/usr/lib/libicucore.A.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, err
	}
	var getInstance func(*int32) uintptr
	purego.RegisterLibFunc(&getInstance, library, "unorm2_getNFKCInstance")
	value := &nativeICUNormalization{}
	purego.RegisterLibFunc(&value.normalize, library, "unorm2_normalize")
	status := int32(0)
	value.instance = getInstance(&status)
	if status > 0 || value.instance == 0 {
		return nil, fmt.Errorf("native NFKC initialization failed: ICU status %d", status)
	}
	return value, nil
})

func platformNFKC(units []uint16, _ *unicodeTables) ([]rune, error) {
	if len(units) == 0 {
		return []rune{}, nil
	}
	if len(units) > math.MaxInt32/2-8 {
		return nil, fmt.Errorf("native NFKC input exceeds ICU length range")
	}
	value, err := nativeICUNormalizer()
	if err != nil {
		return nil, err
	}
	output := make([]uint16, len(units)*2+8)
	status := int32(0)
	length := value.normalize(value.instance, &units[0], int32(len(units)), &output[0], int32(len(output)), &status)
	if status == 15 && length >= 0 && length < math.MaxInt32 {
		// U_BUFFER_OVERFLOW_ERROR returns the required capacity, without
		// discarding or truncating a compatibility expansion.
		output = make([]uint16, int(length)+1)
		status = 0
		length = value.normalize(value.instance, &units[0], int32(len(units)), &output[0], int32(len(output)), &status)
	}
	runtime.KeepAlive(units)
	if status > 0 || length < 0 || int64(length) > int64(len(output)) {
		return nil, fmt.Errorf("native NFKC failed: ICU status %d, length %d", status, length)
	}
	return utf16.Decode(output[:length]), nil
}
