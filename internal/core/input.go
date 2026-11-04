package core

import (
	"fmt"
	"strconv"
	"strings"
)

// decodeInput decodes exactly one explicit escape layer. Ordinary quotes and
// bytes remain literal; invalid escapes fail before any remote write occurs.
// UnquoteChar distinguishes byte escapes (\xFF) from Unicode code points.
func decodeInput(input string) (string, error) {
	originalLength := len(input)
	var out strings.Builder
	for len(input) > 0 {
		if input[0] != '\\' {
			out.WriteByte(input[0])
			input = input[1:]
			continue
		}
		// Both quote escapes are convenient inside command lines. The wrapper is
		// not a quoted Go string, so ordinary quotes need no escaping.
		if strings.HasPrefix(input, `\'`) {
			out.WriteByte('\'')
			input = input[2:]
			continue
		}
		value, multi, tail, err := strconv.UnquoteChar(input, '"')
		if err != nil {
			return "", fmt.Errorf("invalid data_escaped escape at byte %d; nothing sent", originalLength-len(input))
		}
		if multi {
			out.WriteRune(value)
		} else {
			out.WriteByte(byte(value))
		}
		input = tail
	}
	return out.String(), nil
}
