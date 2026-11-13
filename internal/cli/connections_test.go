package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zytyan/keepsess/internal/history"
	"github.com/zytyan/keepsess/internal/testutil"
)

func TestPrivateConnectionStorageWithoutWebHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.WriteFile(filepath.Join(home, ".keepsess.toml"), []byte("[web]\nenabled=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	addr, _ := testutil.SSH(t)
	host, port, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(port)
	request := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "connect", "arguments": map[string]any{"protocol": "ssh", "host": host, "port": n, "username": "user", "password": "private-cli-secret"}}}
	input, _ := json.Marshal(request)
	var output, errout bytes.Buffer
	if err := Run(context.Background(), []string{"serve"}, strings.NewReader(string(input)+"\n"), &output, &errout); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "private-cli-secret") || strings.Contains(errout.String(), "private-cli-secret") {
		t.Fatal("credential in stdout/stderr")
	}
	root := filepath.Join(home, ".keepsess", "history")
	reader, err := history.View(root, "reader")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	page, err := reader.Connections(context.Background(), "", 50)
	if err != nil || len(page.Items) != 1 {
		t.Fatal("successful connection was not persisted with Web disabled", err, errout.String())
	}
	item := page.Items[0]
	record, err := reader.Connection(item.Instance, item.Session, true)
	if err != nil || record.Target.Password != "private-cli-secret" {
		t.Fatal("private password not saved", err)
	}
	events, err := filepath.Glob(filepath.Join(root, item.Instance, "*.jsonl"))
	if err != nil || len(events) != 0 {
		t.Fatal("Web disabled unexpectedly enabled terminal history", err)
	}
}
