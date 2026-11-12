package web

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// The optional browser suite also exercises the pure line/query model in Node.
// Go builds and the default test suite still require no JavaScript runtime.
func TestBrowserSearchModel(t *testing.T) {
	if os.Getenv("KEEPSESS_BROWSER_TEST") != "1" {
		t.Skip("opt in with KEEPSESS_BROWSER_TEST=1")
	}
	node := os.Getenv("KEEPSESS_NODE")
	if node == "" {
		node = "node"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, node, "testdata/search-model.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("model: %v\n%s", err, out)
	}
	t.Log(string(out))
}
