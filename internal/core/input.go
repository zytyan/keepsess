package core

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func textEncoding(name string) (encoding.Encoding, error) {
	switch name {
	case "utf-8":
		return nil, nil
	case "gbk":
		return simplifiedchinese.GBK, nil
	case "shift-jis":
		return japanese.ShiftJIS, nil
	default:
		return nil, errors.New("encoding must be utf-8, gbk or shift-jis")
	}
}
func encodeText(text, name string) ([]byte, error) {
	if !utf8.ValidString(text) {
		return nil, errors.New("invalid Unicode input; use byte escapes")
	}
	enc, err := textEncoding(name)
	if err != nil {
		return nil, err
	}
	if enc == nil {
		return []byte(text), nil
	}
	b, err := enc.NewEncoder().Bytes([]byte(text))
	if err != nil {
		return nil, errors.New("text cannot be represented in configured encoding; nothing sent")
	}
	return b, nil
}
func encodeInput(input string, escaped bool, name string) ([]byte, error) {
	if !escaped {
		return encodeText(input, name)
	}
	var out bytes.Buffer
	for len(input) > 0 {
		i := strings.IndexByte(input, '\\')
		if i < 0 {
			i = len(input)
		}
		if i > 0 {
			b, err := encodeText(input[:i], name)
			if err != nil {
				return nil, err
			}
			out.Write(b)
			input = input[i:]
			continue
		}
		if strings.HasPrefix(input, `\0`) && (len(input) < 4 || input[2] < '0' || input[2] > '7' || input[3] < '0' || input[3] > '7') {
			out.WriteByte(0)
			input = input[2:]
			continue
		}
		if strings.HasPrefix(input, `\'`) {
			out.WriteByte('\'')
			input = input[2:]
			continue
		}
		value, multi, tail, err := strconv.UnquoteChar(input, '"')
		if err != nil {
			return nil, errors.New("invalid escaped input; nothing sent")
		}
		if multi {
			b, err := encodeText(string(value), name)
			if err != nil {
				return nil, err
			}
			out.Write(b)
		} else {
			out.WriteByte(byte(value))
		}
		input = tail
	}
	return out.Bytes(), nil
}

// decodeWindow never consumes an incomplete character while more bytes may
// arrive. bounds maps every displayed byte boundary to its raw-stream offset;
// an escaped invalid byte is consumed/displayed as one indivisible unit.
type decodedWindow struct {
	text                string
	bounds              []int
	consumed            int
	incomplete, limited bool
	minimum             int
	err                 error
}

func decodeWindow(raw []byte, c Config, final bool) decodedWindow {
	d := decodedWindow{bounds: []int{0}}
	enc, _ := textEncoding(c.Encoding)
	var out strings.Builder
	for pos := 0; pos < len(raw); {
		width := 1
		valid := true
		var piece string
		if c.Encoding == "utf-8" {
			if !utf8.FullRune(raw[pos:]) && !final {
				d.incomplete = true
				break
			}
			r, w := utf8.DecodeRune(raw[pos:])
			width = w
			valid = !(r == utf8.RuneError && w == 1)
			if valid {
				piece = string(r)
			}
		} else {
			b := raw[pos]
			if c.Encoding == "gbk" {
				if b >= 0x81 && b <= 0xfe {
					width = 2
				} else if b > 0x80 {
					valid = false
				}
			} else {
				if (b >= 0x81 && b <= 0x9f) || (b >= 0xe0 && b <= 0xfc) {
					width = 2
				} else if b >= 0x80 && (b < 0xa1 || b > 0xdf) {
					valid = false
				}
			}
			if width > len(raw)-pos {
				if !final {
					d.incomplete = true
					break
				}
				valid = false
				width = 1
			}
			if valid {
				text, err := enc.NewDecoder().Bytes(raw[pos : pos+width])
				piece = string(text)
				if err != nil || strings.ContainsRune(piece, utf8.RuneError) {
					valid = false
					width = 1
				}
			}
		}
		if !valid {
			width = 1
			switch c.OutputDecodeErrorAction {
			case "error":
				d.err = errors.New("invalid output byte; unread data retained, override output_decode_error_action to inspect")
				d.text = out.String()
				return d
			case "to_hex":
				piece = fmt.Sprintf("<%02X>", raw[pos])
			default:
				piece = fmt.Sprintf(`\x%02X`, raw[pos])
			}
		}
		if int64(out.Len()+len(piece)) > c.MaxOutputBytes {
			d.limited = true
			d.minimum = len(piece)
			break
		}
		pos += width
		out.WriteString(piece)
		for j := 0; j < len(piece); j++ {
			d.bounds = append(d.bounds, pos)
		}
		d.consumed = pos
	}
	d.text = out.String()
	return d
}
