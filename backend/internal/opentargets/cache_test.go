package opentargets

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"testing"
)

func TestApplicationCachesRemainBoundedAndUnissuedIconsCannotProbePaths(t *testing.T) {
	home, row := fixture(t, "darwin")
	var first, last string
	var firstID string
	for i := 0; i < 70; i++ {
		file := filepath.Join(row.CWD, fmt.Sprintf("file%02d.txt", i))
		ownedFile(t, file, 0600)
		apps := []Application{}
		for j := 0; j < 5; j++ {
			apps = append(apps, app(fmt.Sprintf("/Applications/Set%d-App%d.app", i, j), fmt.Sprintf("app%d-%d", i, j), fmt.Sprintf("App%d-%d", i, j), false, float64(j)))
		}
		row.Native.Applications[file] = jsonApps(apps, nil)
		row.Operations = append(row.Operations, queryOp(file))
		if i == 0 {
			first = file
			firstID = applicationID(apps[0])
		}
		last = file
	}
	row.Operations = append(row.Operations, iconOp(firstID), queryOp(last), queryOp(first))
	s, out := runScenario(t, home, row)
	if len(s.paths) != maxCachedPaths || len(s.registry) != maxRegisteredApplications || len(out.Queries) != 71 {
		t.Fatalf("bounded caches paths=%d registry=%d queries=%d", len(s.paths), len(s.registry), len(out.Queries))
	}
	if out.Observations[70].Status != 404 {
		t.Fatalf("evicted app still resolved %+v", out.Observations[70])
	}
}
func TestTargetRefreshFailedLaunchAndIconSizeCache(t *testing.T) {
	home, row := fixture(t, "darwin")
	file := filepath.Join(row.CWD, "file.txt")
	ownedFile(t, file, 0600)
	row.Native.Exists = []string{"/Applications/Cursor.app", "/Applications/Cursor.app/Contents/Resources/Cursor.icns"}
	row.Native.Icons["/Applications/Cursor.app/Contents/Resources/Cursor.icns"] = base64.StdEncoding.EncodeToString([]byte("cursor PNG"))
	row.Native.LaunchCode = 1
	row.Operations = []operation{{Kind: "list"}, {Kind: "list", Force: true}, openOp("cursor", file), iconOp("cursor")}
	s, out := runScenario(t, home, row)
	if out.Observations[2].Status != 500 {
		t.Fatalf("launch failed but not surfaced %+v", out.Observations[2])
	}
	for _, size := range []float64{1, 16.4, 256, 1000} {
		if _, err := s.GetIcon(context.Background(), "cursor", size); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.icons) != 3 {
		t.Fatalf("icon size normalization/cache %v", s.icons)
	}
}
