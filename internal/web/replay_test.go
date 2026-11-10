package web

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zytyan/keepsess/internal/core"
)

func TestBrowserBulkReplay(t *testing.T)  { runBrowserReplay(t, 1200) }
func TestBrowserLargeReplay(t *testing.T) { runBrowserReplay(t, 12000) }
func runBrowserReplay(t *testing.T, records int) {
	if os.Getenv("KEEPSESS_BROWSER_TEST") != "1" {
		t.Skip("opt in with KEEPSESS_BROWSER_TEST=1")
	}
	s, _, st := setup(t, Options{Listen: "127.0.0.1:0", BasePath: "/nested/"})
	st.Emit(core.Event{Kind: "session", Session: "s1", Mode: "exec", Protocol: "ssh", Encoding: "utf-8", Status: core.Result{Connected: true}})
	st.Emit(core.Event{Kind: "command", Session: "s1", Handle: "task1", Command: "bulk-history", Status: core.Result{Connected: true, State: "running"}})
	for i := 0; i < records; i++ {
		st.Emit(core.Event{Kind: "output", Session: "s1", Handle: "task1", Encoding: "utf-8", Data: []byte(fmt.Sprintf("row %04d 中文%s\r\n", i, strings.Repeat(" log", 32)))})
		if i%50 == 0 {
			waitRecorded(t, st)
		}
	}
	st.Emit(core.Event{Kind: "task", Session: "s1", Handle: "task1", Status: core.Result{Connected: true, State: "finished"}})
	waitRecorded(t, st)
	node := os.Getenv("KEEPSESS_NODE")
	if node == "" {
		node = "node"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, node, "testdata/replay.cjs", s.Info().URL, strconv.Itoa(records)).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Log(string(out))
}
