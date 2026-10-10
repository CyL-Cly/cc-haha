package projectpath

import "testing"

func TestPinnedZigOriginalVectors(t *testing.T) {
	vectors := []struct {
		seed  uint64
		input string
		want  uint64
	}{
		{0, "", 0x409638ee2bde459},
		{1, "a", 0xa8412d091b5fe0a9},
		{2, "abc", 0x32dd92e4b2915153},
		{3, "message digest", 0x8619124089a3a16b},
		{4, "abcdefghijklmnopqrstuvwxyz", 0x7a43afb61d7f5f40},
		{5, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789", 0xff42329b90e50d58},
		{6, "12345678901234567890123456789012345678901234567890123456789012345678901234567890", 0xc39cab13b115aad3},
	}
	for _, row := range vectors {
		if got := wyhash(row.seed, []byte(row.input)); got != row.want {
			t.Fatalf("seed%d actual%x expected%x", row.seed, got, row.want)
		}
	}
}
