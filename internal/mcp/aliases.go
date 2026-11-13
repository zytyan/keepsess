package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/zytyan/keepsess/internal/core"
)

type parameterAlias struct {
	canonical string
	preferred string
	aliases   []string
}

// An explicit vocabulary, never fuzzy spelling or unit conversion. Secrets,
// route/trust fields and semantically different settings are not aliased.
func parameterAliases(tool string) []parameterAlias {
	out := []parameterAlias{
		{"read_total_timeout_ms", "read_timeout_ms", []string{"read_timeout_ms"}},
		{"read_idle_timeout_ms", "read_idle_timeout_ms", []string{"read_idle_ms"}},
		{"session_idle_timeout_ms", "session_idle_timeout_ms", []string{"session_idle_ms"}},
		{"input_encode_error_action", "encode_errors", []string{"encode_errors", "encode_error", "input_encode_errors"}},
		{"output_decode_error_action", "decode_errors", []string{"decode_errors", "decode_error", "output_decode_errors"}},
		{"include_pattern", "include_pattern", []string{"include_regex"}},
		{"exclude_pattern", "exclude_pattern", []string{"exclude_regex"}},
		{"end_pattern", "end_pattern", []string{"end_regex"}},
	}
	completion := parameterAlias{"exec_total_timeout_ms", "exec_timeout_ms", []string{"exec_timeout_ms", "execution_timeout_ms"}}
	if tool == "exec" || tool == "task_wait" {
		completion.preferred = "timeout_ms"
		completion.aliases = append(completion.aliases, "timeout_ms")
	}
	out = append(out, completion)
	if tool != "configuration" {
		out = append(out,
			parameterAlias{"command", "command", []string{"cmd"}},
			parameterAlias{"data", "data", []string{"text"}},
			parameterAlias{"session", "session", []string{"session_id"}},
		)
	}
	if tool != "configuration" && tool != "tunnel_close" {
		out = append(out, parameterAlias{"handle", "handle", []string{"task_id"}})
	}
	return out
}

func aliasValue(key string, raw json.RawMessage) (any, error) {
	if strings.HasSuffix(key, "_ms") || strings.HasSuffix(key, "_bytes") {
		return core.ConfigInteger(raw)
	}
	var value any
	err := json.Unmarshal(raw, &value)
	return value, err
}

func normalizeAliases(tool string, props map[string]any, keys map[string]json.RawMessage, configOnly bool) error {
	configs := core.ConfigDescriptions()
	for _, group := range parameterAliases(tool) {
		if props[group.canonical] == nil {
			continue
		}
		if _, ok := configs[group.canonical]; configOnly && !ok {
			continue
		}
		names := append([]string{group.canonical}, group.aliases...)
		var selected json.RawMessage
		var selectedName string
		for _, name := range names {
			raw, ok := keys[name]
			if !ok {
				continue
			}
			if selected != nil {
				first, err := aliasValue(group.canonical, selected)
				if err != nil {
					return fmt.Errorf("invalid %s; no operation was performed", selectedName)
				}
				next, err := aliasValue(group.canonical, raw)
				if err != nil {
					return fmt.Errorf("invalid %s; no operation was performed", name)
				}
				if !reflect.DeepEqual(first, next) {
					return fmt.Errorf("conflicting arguments %s and %s; supply %s alone or equal values. No application input was sent; no operation was performed", selectedName, name, group.preferred)
				}
			} else {
				selected, selectedName = raw, name
			}
		}
		if selected != nil {
			for _, name := range group.aliases {
				delete(keys, name)
			}
			keys[group.canonical] = selected
		}
	}
	return nil
}

// Discovery and help share this projection: one preferred field per meaning.
func preferredProperties(tool string, props map[string]any) map[string]any {
	result := make(map[string]any, len(props))
	configs := core.ConfigDescriptions()
	for key, field := range props {
		if _, config := configs[key]; config && tool != "configuration" && !applicableConfig(tool, key) {
			continue
		}
		if key == "timeout_ms" {
			continue
		} // canonical completion field is projected below
		result[key] = documentedField(key, field.(map[string]any))
	}
	for _, group := range parameterAliases(tool) {
		raw, ok := result[group.canonical]
		if !ok {
			continue
		}
		field := raw.(map[string]any)
		names := append([]string{group.canonical}, group.aliases...)
		aliases := []string{}
		for _, name := range names {
			if name != group.preferred {
				aliases = append(aliases, name)
			}
		}
		if group.preferred == "timeout_ms" {
			field = cloneMap(props["timeout_ms"].(map[string]any))
		}
		field["description"] = field["description"].(string) + " Accepted aliases: " + strings.Join(aliases, ", ") + ". Multiple names must have equal values; numeric arithmetic is compared after evaluation. Conflicts reject before any operation."
		delete(result, group.canonical)
		result[group.preferred] = field
	}
	return result
}

func preferredParameter(tool, name string) string {
	for _, group := range parameterAliases(tool) {
		if name == group.canonical {
			return group.preferred
		}
		for _, alias := range group.aliases {
			if alias == name {
				return group.preferred
			}
		}
	}
	return name
}

// AcceptedParameterAliases supports generated clients without expanding MCP discovery.
func AcceptedParameterAliases(tool string) map[string]string {
	props := definitions[tool]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	out := map[string]string{}
	for _, group := range parameterAliases(tool) {
		if props[group.canonical] == nil {
			continue
		}
		for _, alias := range group.aliases {
			out[alias] = group.canonical
		}
	}
	return out
}
