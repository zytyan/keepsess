package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/big"
	"regexp"
	"strings"
	"time"
)

type Config struct {
	ConnectTimeoutMS        int64  `json:"connect_timeout_ms"`
	WriteTimeoutMS          int64  `json:"write_timeout_ms"`
	SessionIdleTimeoutMS    int64  `json:"session_idle_timeout_ms"`
	ReadTotalTimeoutMS      int64  `json:"read_total_timeout_ms"`
	ReadIdleTimeoutMS       int64  `json:"read_idle_timeout_ms"`
	TCPKeepaliveMS          int64  `json:"tcp_keepalive_ms"`
	LineEnd                 string `json:"line_end"`
	Encoding                string `json:"encoding"`
	InputEncodeErrorAction  string `json:"input_encode_error_action"`
	OutputDecodeErrorAction string `json:"output_decode_error_action"`
	MaxInputBytes           int64  `json:"max_input_bytes"`
	MaxOutputBytes          int64  `json:"max_output_bytes"`
	BackpressureBytes       int64  `json:"backpressure_bytes"`
	EndPattern              string `json:"end_pattern"`
}
type ConfigPatch map[string]json.RawMessage

type ConfigField struct {
	Value       any    `json:"value"`
	Description string `json:"description"`
	Unit        string `json:"unit,omitempty"`
}
type ConfigResult struct {
	Session      string                 `json:"session"`
	Config       Config                 `json:"config"`
	Fields       map[string]ConfigField `json:"fields,omitempty"`
	Capabilities map[string]any         `json:"capabilities,omitempty"`
}

var configDescriptions = map[string]string{
	"connect_timeout_ms":         "Connect, authentication and initial shell setup deadline. On an established session this is stored only; explicitly reuse config on a new connect.",
	"write_timeout_ms":           "Write/channel setup deadline; failed writes close transport and may have executed. Maximum 110000ms.",
	"session_idle_timeout_ms":    "Reclaim session and buffers after no operations; 0 disables. Active readers/writers or running exec are excluded.",
	"read_total_timeout_ms":      "Total read deadline from read start; 1..110000ms. Does not close transport.",
	"read_idle_timeout_ms":       "Read silence deadline from read start, reset by endpoint bytes; 0 disables. Default 500ms.",
	"tcp_keepalive_ms":           "OS TCP keepalive period, 0 disables. Actual probe/failure timing depends on OS.",
	"line_end":                   "Visible escape text decoded once to bytes and appended by sendline; \\0 is NUL. Empty appends nothing.",
	"encoding":                   "Text encoding: utf-8, gbk or shift-jis; byte escapes bypass encoding.",
	"input_encode_error_action":  "error only: unencodable text rejects the entire input before writing.",
	"output_decode_error_action": "Invalid bytes: to_escape_bytes (\\xNN), to_hex (<NN>), or error (leave unread).",
	"max_input_bytes":            "1..1048576 encoded bytes, including line ending.",
	"max_output_bytes":           "1..65536 UTF-8 output bytes, including invalid-byte representations. Default 8192.",
	"backpressure_bytes":         "0 disables threshold. Otherwise reject a new write while OS TCP queued bytes >= threshold; no user-space queue. Platform support required.",
	"end_pattern":                "Go RE2 over decoded unread output; empty disables. Match is observation, not command completion.",
}

func DefaultConfig(protocol string) Config {
	end := `\r`
	if protocol == "telnet" {
		end = `\r\n`
	}
	return Config{ConnectTimeoutMS: 10000, WriteTimeoutMS: 10000, SessionIdleTimeoutMS: 1800000, ReadTotalTimeoutMS: 30000, ReadIdleTimeoutMS: 500, TCPKeepaliveMS: 15000, LineEnd: end, Encoding: "utf-8", InputEncodeErrorAction: "error", OutputDecodeErrorAction: "to_escape_bytes", MaxInputBytes: 4096, MaxOutputBytes: 8192}
}
func (c Config) Duration(ms int64) time.Duration { return time.Duration(ms) * time.Millisecond }
func applyConfig(c Config, patch ConfigPatch, override bool) (Config, error) {
	for k, v := range patch {
		if _, ok := configDescriptions[k]; !ok {
			return c, fmt.Errorf("unknown config parameter %q", k)
		}
		if override && (k == "connect_timeout_ms" || k == "session_idle_timeout_ms" || k == "tcp_keepalive_ms") {
			return c, fmt.Errorf("%s is connection configuration; use sess_configure", k)
		}
		var err error
		switch k {
		case "connect_timeout_ms":
			c.ConnectTimeoutMS, err = integer(v)
		case "write_timeout_ms":
			c.WriteTimeoutMS, err = integer(v)
		case "session_idle_timeout_ms":
			c.SessionIdleTimeoutMS, err = integer(v)
		case "read_total_timeout_ms":
			c.ReadTotalTimeoutMS, err = integer(v)
		case "read_idle_timeout_ms":
			c.ReadIdleTimeoutMS, err = integer(v)
		case "tcp_keepalive_ms":
			c.TCPKeepaliveMS, err = integer(v)
		case "max_input_bytes":
			c.MaxInputBytes, err = integer(v)
		case "max_output_bytes":
			c.MaxOutputBytes, err = integer(v)
		case "backpressure_bytes":
			c.BackpressureBytes, err = integer(v)
		default:
			var str string
			if string(v) == "null" {
				err = errors.New("null is not a config value")
			} else {
				err = json.Unmarshal(v, &str)
			}
			if err == nil {
				switch k {
				case "line_end":
					c.LineEnd = str
				case "encoding":
					c.Encoding = str
				case "input_encode_error_action":
					c.InputEncodeErrorAction = str
				case "output_decode_error_action":
					c.OutputDecodeErrorAction = str
				case "end_pattern":
					c.EndPattern = str
				}
			}
		}
		if err != nil {
			return c, fmt.Errorf("invalid %s: %w", k, err)
		}
	}
	c.Encoding = strings.ToLower(c.Encoding)
	switch c.Encoding {
	case "utf8":
		c.Encoding = "utf-8"
	case "shift_jis", "shiftjis", "sjis":
		c.Encoding = "shift-jis"
	}
	if c.ConnectTimeoutMS < 1 || c.ConnectTimeoutMS > 110000 || c.WriteTimeoutMS < 1 || c.WriteTimeoutMS > 110000 || c.ReadTotalTimeoutMS < 1 || c.ReadTotalTimeoutMS > 110000 {
		return c, errors.New("connect/write/read_total timeouts must be 1..110000ms")
	}
	if c.ReadIdleTimeoutMS < 0 || c.ReadIdleTimeoutMS > 110000 || c.SessionIdleTimeoutMS < 0 || c.SessionIdleTimeoutMS > 31536000000 || c.TCPKeepaliveMS < 0 || c.TCPKeepaliveMS > 86400000 {
		return c, errors.New("idle/keepalive timeout outside allowed range")
	}
	if c.MaxInputBytes < 1 || c.MaxInputBytes > MaxInputLimit || c.MaxOutputBytes < 1 || c.MaxOutputBytes > ResultLimit || c.BackpressureBytes < 0 || c.BackpressureBytes > 1<<30 {
		return c, errors.New("input/output/backpressure byte limit outside allowed range")
	}
	if c.BackpressureBytes > 0 && !queueSupported() {
		return c, errors.New("OS TCP queue query unsupported on this platform; backpressure_bytes must be 0")
	}
	if _, err := textEncoding(c.Encoding); err != nil {
		return c, err
	}
	if c.InputEncodeErrorAction != "error" {
		return c, errors.New("input_encode_error_action must be error")
	}
	switch c.OutputDecodeErrorAction {
	case "to_escape_bytes", "to_hex", "error":
	default:
		return c, errors.New("invalid output_decode_error_action")
	}
	if len(c.EndPattern) > 1024 {
		return c, errors.New("end_pattern exceeds 1024 bytes")
	}
	if _, err := regexp.Compile(c.EndPattern); err != nil {
		return c, fmt.Errorf("invalid end_pattern: %w", err)
	}
	end, err := encodeInput(c.LineEnd, true, "utf-8")
	if err != nil {
		return c, fmt.Errorf("invalid line_end: %w", err)
	}
	if len(end) > 1024 {
		return c, errors.New("line_end exceeds 1024 decoded bytes")
	}
	return c, nil
}
func integer(raw json.RawMessage) (int64, error) {
	str := string(raw)
	if strings.HasPrefix(str, `"`) {
		if err := json.Unmarshal(raw, &str); err != nil {
			return 0, err
		}
	}
	if len(str) > 128 {
		return 0, errors.New("expression too long")
	}
	expr, err := parser.ParseExpr(str)
	if err != nil {
		return 0, errors.New("expected integer or arithmetic string")
	}
	n, err := evalInteger(expr, 0)
	if err != nil {
		return 0, err
	}
	return n.Int64(), nil
}
func evalInteger(e ast.Expr, depth int) (*big.Int, error) {
	if depth > 16 {
		return nil, errors.New("expression too deep")
	}
	result := new(big.Int)
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.INT {
			return nil, errors.New("integer literals only")
		}
		if _, ok := result.SetString(v.Value, 10); !ok {
			return nil, errors.New("decimal integers only")
		}
	case *ast.ParenExpr:
		return evalInteger(v.X, depth+1)
	case *ast.UnaryExpr:
		n, err := evalInteger(v.X, depth+1)
		if err != nil {
			return nil, err
		}
		result.Set(n)
		if v.Op == token.SUB {
			result.Neg(result)
		} else if v.Op != token.ADD {
			return nil, errors.New("unsupported arithmetic operator")
		}
	case *ast.BinaryExpr:
		a, err := evalInteger(v.X, depth+1)
		if err != nil {
			return nil, err
		}
		b, err := evalInteger(v.Y, depth+1)
		if err != nil {
			return nil, err
		}
		switch v.Op {
		case token.ADD:
			result.Add(a, b)
		case token.SUB:
			result.Sub(a, b)
		case token.MUL:
			result.Mul(a, b)
		case token.QUO:
			if b.Sign() == 0 {
				return nil, errors.New("division by zero")
			}
			rem := new(big.Int)
			result.QuoRem(a, b, rem)
			if rem.Sign() != 0 {
				return nil, errors.New("division must yield an integer")
			}
		default:
			return nil, errors.New("only + - * / and parentheses allowed")
		}
	default:
		return nil, errors.New("only integer arithmetic allowed")
	}
	if result.BitLen() > 53 {
		return nil, errors.New("integer expression out of range")
	}
	return result, nil
}
func configInfo(id string, c Config) ConfigResult {
	b, _ := json.Marshal(c)
	var values map[string]any
	_ = json.Unmarshal(b, &values)
	fields := make(map[string]ConfigField, len(values))
	for k, v := range values {
		unit := ""
		if strings.HasSuffix(k, "_ms") {
			unit = "ms"
		} else if strings.HasSuffix(k, "_bytes") {
			unit = "bytes"
		}
		fields[k] = ConfigField{v, configDescriptions[k], unit}
	}
	return ConfigResult{Session: id, Config: c, Fields: fields, Capabilities: map[string]any{"tcp_queue_query": queueSupported(), "tcp_queue_metric": queueMetric(), "encodings": []string{"utf-8", "gbk", "shift-jis"}, "hard_call_limit_ms": int(CallLimit / time.Millisecond), "history_bytes": HistoryLimit, "connect_timeout_applies": "connect_only; explicitly reuse config for a new connection"}}
}

func ConfigDescriptions() map[string]string {
	out := make(map[string]string, len(configDescriptions))
	for k, v := range configDescriptions {
		out[k] = v
	}
	return out
}
