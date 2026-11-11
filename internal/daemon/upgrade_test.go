package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zytyan/keepsess/internal/settings"
)

func TestLifecycleExclusiveAndStaleRecovery(t *testing.T) {
	c, e := settings.LoadHome(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	a, e := gate(c)
	if e != nil {
		t.Fatal(e)
	}
	if b, e := gate(c); e == nil {
		b.Close()
		t.Fatal("overlapping lifecycle locks")
	}
	if e = atomicJSON(ticketPath(c), upgradeTicket{Token: "secret", Candidate: strings.Repeat("a", 64)}); e != nil {
		t.Fatal(e)
	}
	if e = recoverUpgrade(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(ticketPath(c)); !os.IsNotExist(e) {
		t.Fatal("stale ticket retained", e)
	}
	a.Close()
	b, e := gate(c)
	if e != nil {
		t.Fatal(e)
	}
	b.Close()
}
func TestSelectionNeverFallsBackToCallerOnCorruption(t *testing.T) {
	c, e := settings.LoadHome(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(c.RuntimeDir(), 0700); e != nil {
		t.Fatal(e)
	}
	if e = atomicJSON(filepath.Join(c.RuntimeDir(), "selected.json"), selection{Current: "../../bad"}); e != nil {
		t.Fatal(e)
	}
	if _, e = selectedBinary(c); e == nil {
		t.Fatal("invalid selected binary accepted")
	}
	hash := strings.Repeat("a", 64)
	p := versionPath(c, hash)
	os.MkdirAll(filepath.Dir(p), 0700)
	os.WriteFile(p, []byte("bad"), 0700)
	atomicJSON(filepath.Join(c.RuntimeDir(), "selected.json"), selection{Current: hash})
	if _, e = selectedBinary(c); e == nil {
		t.Fatal("digest mismatch accepted")
	}
}
func TestLegacyRuntimeOnlyManagementReader(t *testing.T) {
	c, e := settings.LoadHome(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	os.MkdirAll(c.RuntimeDir(), 0700)
	r := Runtime{Protocol: 1, PID: 123, Token: strings.Repeat("ab", 32), Endpoint: "http://127.0.0.1:12345/internal/"}
	if e = atomicJSON(runtimePath(c), r); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadRuntime(c); e == nil {
		t.Fatal("legacy protocol accepted by Agent")
	}
	if _, e = readRuntime(c, false); e != nil {
		t.Fatal("legacy management unavailable", e)
	}
}
