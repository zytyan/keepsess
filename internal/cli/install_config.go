package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
	"github.com/tailscale/hujson"
)

// Only a recognizable sessmcp stdio entry is upgraded automatically. Preserve
// its env, logging arguments, timeouts and approval settings without enabling it.
func ownedServer(entry map[string]any) bool {
	command, ok := entry["command"].(string)
	if !ok {
		return false
	}
	base := path.Base(strings.ReplaceAll(command, `\`, "/"))
	if base != "sessmcp" && base != "sessmcp.exe" {
		return false
	}
	if _, exists := entry["url"]; exists {
		return false
	}
	if typ, exists := entry["type"]; exists && typ != "stdio" {
		return false
	}
	switch args := entry["args"].(type) {
	case []any:
		return len(args) > 0 && args[0] == "serve"
	case []string:
		return len(args) > 0 && args[0] == "serve"
	}
	return false
}
func parseInstallJSON(b []byte) (hujson.Value, error) {
	b = bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf})
	if len(bytes.TrimSpace(b)) == 0 {
		b = []byte("{}\n")
	}
	v, err := hujson.Parse(b)
	if err != nil {
		return v, errors.New("invalid JSON/JSONC configuration; nothing installed")
	}
	if _, ok := v.Value.(*hujson.Object); !ok {
		return v, errors.New("configuration root must be an object")
	}
	if err = uniqueJSONKeys(&v); err != nil {
		return v, err
	}
	return v, nil
}
func uniqueJSONKeys(v *hujson.Value) error {
	switch x := v.Value.(type) {
	case *hujson.Object:
		seen := map[string]bool{}
		for i := range x.Members {
			m := &x.Members[i]
			var k string
			_ = json.Unmarshal([]byte(m.Name.Value.(hujson.Literal)), &k)
			if seen[k] {
				return errors.New("duplicate JSON configuration key; nothing installed")
			}
			seen[k] = true
			if err := uniqueJSONKeys(&m.Value); err != nil {
				return err
			}
		}
	case *hujson.Array:
		for i := range x.Elements {
			if err := uniqueJSONKeys(&x.Elements[i]); err != nil {
				return err
			}
		}
	}
	return nil
}
func installJSON(b []byte, client, command string) ([]byte, error) {
	root, err := parseInstallJSON(b)
	if err != nil {
		return nil, err
	}
	key := "mcpServers"
	if client == "vscode" {
		key = "servers"
	}
	if root.Find("/"+key) == nil {
		obj := root.Value.(*hujson.Object)
		obj.Members = append(obj.Members, hujson.ObjectMember{Name: hujson.Value{Value: hujson.String(key)}, Value: hujson.Value{Value: &hujson.Object{}}})
	}
	servers := root.Find("/" + key)
	if _, ok := servers.Value.(*hujson.Object); !ok {
		return nil, fmt.Errorf("%s must be an object; nothing installed", key)
	}
	var entry map[string]any
	current := root.Find("/" + key + "/sessmcp")
	if current != nil {
		plain := current.Clone()
		plain.Standardize()
		if err = json.Unmarshal(plain.Pack(), &entry); err != nil || entry == nil || !ownedServer(entry) {
			return nil, errors.New("existing sessmcp entry is not a recognizable sessmcp serve command; rename it before installing")
		}
		if entry["command"] == command && (client != "vscode" || entry["type"] == "stdio") {
			return b, nil
		}
		current.Find("/command").Value = hujson.String(command)
		if client == "vscode" && current.Find("/type") == nil {
			current.Value.(*hujson.Object).Members = append(current.Value.(*hujson.Object).Members, hujson.ObjectMember{Name: hujson.Value{Value: hujson.String("type")}, Value: hujson.Value{Value: hujson.String("stdio")}})
		}
	} else {
		entry = map[string]any{"command": command, "args": []string{"serve"}}
		if client == "vscode" {
			entry["type"] = "stdio"
		}
		raw, _ := json.Marshal(entry)
		value, _ := hujson.Parse(raw)
		obj := servers.Value.(*hujson.Object)
		obj.Members = append(obj.Members, hujson.ObjectMember{Name: hujson.Value{Value: hujson.String("sessmcp")}, Value: value})
	}
	root.Format()
	out := root.Pack()
	if len(out) == 0 || out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return out, nil
}
func installTOML(b []byte, command string) ([]byte, error) {
	var doc map[string]any
	if err := toml.Unmarshal(b, &doc); err != nil {
		return nil, errors.New("invalid TOML configuration; nothing installed")
	}
	var entry map[string]any
	if raw, ok := doc["mcp_servers"]; ok {
		servers, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("mcp_servers must be a TOML table")
		}
		if raw, exists := servers["sessmcp"]; exists {
			entry, ok = raw.(map[string]any)
			if !ok || !ownedServer(entry) {
				return nil, errors.New("existing sessmcp entry is not a recognizable sessmcp serve command; rename it before installing")
			}
		}
	}
	if entry == nil {
		// Append a standard section, then validate the WHOLE result. Inline parent
		// tables cannot be extended; report that before any files are touched.
		section, err := toml.Marshal(map[string]any{"mcp_servers": map[string]any{"sessmcp": map[string]any{"command": command, "args": []string{"serve"}, "tool_timeout_sec": 120}}})
		if err != nil {
			return nil, err
		}
		out := append(append([]byte(nil), b...), []byte("\n")...)
		out = append(out, section...)
		var check map[string]any
		if err = toml.Unmarshal(out, &check); err != nil {
			return nil, errors.New("cannot extend this TOML layout without rewriting existing tables; use a standard [mcp_servers] layout")
		}
		return out, nil
	}
	if entry["command"] == command {
		return b, nil
	}
	// Locate only the scalar command in the syntax tree. This preserves every
	// unrelated byte, including comments, multiline strings and nested tables.
	replacement, err := toml.Marshal(map[string]string{"command": command})
	if err != nil {
		return nil, err
	}
	value := bytes.TrimSpace(bytes.SplitN(replacement, []byte("="), 2)[1])
	var parser unstable.Parser
	parser.Reset(b)
	var table []string
	for parser.NextExpression() {
		n := parser.Expression()
		switch n.Kind {
		case unstable.Table, unstable.ArrayTable:
			table = tomlKey(n)
		case unstable.KeyValue:
			if offset, length, ok := findTOMLCommand(n, table); ok {
				out := append([]byte(nil), b[:offset]...)
				out = append(out, value...)
				out = append(out, b[offset+length:]...)
				var check map[string]any
				if err = toml.Unmarshal(out, &check); err != nil {
					return nil, errors.New("unable to preserve TOML layout; nothing installed")
				}
				return out, nil
			}
		}
	}
	return nil, errors.New("cannot locate sessmcp command in TOML; nothing installed")
}
func tomlKey(n *unstable.Node) []string {
	var parts []string
	it := n.Key()
	for it.Next() {
		parts = append(parts, string(it.Node().Data))
	}
	return parts
}
func findTOMLCommand(n *unstable.Node, parent []string) (int, int, bool) {
	key := append(append([]string(nil), parent...), tomlKey(n)...)
	v := n.Value()
	if strings.Join(key, "\x00") == "mcp_servers\x00sessmcp\x00command" && v.Kind == unstable.String {
		return int(v.Raw.Offset), int(v.Raw.Length), true
	}
	if v.Kind == unstable.InlineTable {
		it := v.Children()
		for it.Next() {
			child := it.Node()
			if child.Kind == unstable.KeyValue {
				if offset, length, ok := findTOMLCommand(child, key); ok {
					return offset, length, true
				}
			}
		}
	}
	return 0, 0, false
}
