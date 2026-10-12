package onboard

import (
	"os/exec"
	"testing"
)

func TestLocalReviewForm(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for browser form regression checks")
	}
	out, err := exec.Command(node, "testdata/reviews.test.cjs", "web/reviews.js").CombinedOutput()
	if err != nil {
		t.Fatalf("local review form: %v\n%s", err, out)
	}
}
