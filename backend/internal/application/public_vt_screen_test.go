package application

import (
	"os/exec"
	"testing"
)

func TestPublicVTScreenPreservesRowsOutsidePhysicalScrollingRegion(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	program := `from vt_screen import VTScreen
s=VTScreen(5,5)
s.feed(b'A\r\nB\r\nC\r\nD\r\nE')
s.feed(b'\x1b[2;4r\x1b[4;1H\n')
assert s.text().splitlines()==['A','C','D','','E'],s.text()
s.feed(b'\x1b[3;1H\x1b[L')
assert s.text().splitlines()==['A','C','','D','E'],s.text()
s.feed(b'\x1b[2;1H\x1b[M')
assert s.text().splitlines()==['A','','D','','E'],s.text()
s.feed(b'\x1b[2;1H\x1bM')
assert s.text().splitlines()==['A','','','D','E'],s.text()
s.feed(b'\x1b[S')
assert s.text().splitlines()==['A','','D','','E'],s.text()
s.feed(b'\x1b[T')
assert s.text().splitlines()==['A','','','D','E'],s.text()
print('real terminal regional LF/RI/IL/DL/S/T projection')`
	cmd := exec.Command(python, "-c", program)
	cmd.Dir = "testdata"
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
}
