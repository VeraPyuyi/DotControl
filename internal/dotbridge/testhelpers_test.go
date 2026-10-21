package dotbridge

import (
	"path/filepath"
	"testing"
)

// physicalTemp resolves the test's temporary root: macOS temporary directories
// sit under the system /var symlink, which the bridge deliberately rejects as
// an ancestor. Production symlink rejection is unchanged.
func physicalTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
