package core

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const ViewScanLimit = 64 << 10
const ViewLineLimit = 16 << 10

type outputView struct {
	decodedWindow
	filteredBytes, filteredLines, strippedBytes int64
	fragments                                   bool
}

// Incomplete control sequences stay in raw history until the next read. No
// parser state is lost across page boundaries; history reads remain independent.
type ansiRemoval struct {
	end   int
	bytes int64
}

func cleanANSI(d decodedWindow, strip, final bool) (string, []int, []ansiRemoval) {
	if !strip {
		return d.text, d.bounds, nil
	}
	var out strings.Builder
	bounds := []int{0}
	var stripped []ansiRemoval
	for i := 0; i < len(d.text); {
		if d.text[i] == 27 {
			end, complete := ansiEnd(d.text, i)
			if !complete && !final {
				break
			}
			stripped = append(stripped, ansiRemoval{end: d.bounds[end], bytes: int64(end - i)})
			bounds[len(bounds)-1] = d.bounds[end]
			i = end
			continue
		}
		out.WriteByte(d.text[i])
		i++
		bounds = append(bounds, d.bounds[i])
	}
	return out.String(), bounds, stripped
}

func ansiEnd(s string, start int) (int, bool) {
	i := start + 1
	if i >= len(s) {
		return i, false
	}
	switch s[i] {
	case '[':
		for i++; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1, true
			}
		}
	case ']', 'P', 'X', '^', '_':
		osc := s[i] == ']'
		for i++; i < len(s); i++ {
			if osc && s[i] == 7 {
				return i + 1, true
			}
			if s[i] == 27 && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2, true
			}
		}
	default:
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) {
			return i + 1, true
		}
	}
	return len(s), false
}

func projectOutput(d decodedWindow, c Config, final, ansiFinal bool) outputView {
	text, bounds, stripped := cleanANSI(d, c.StripANSI, ansiFinal)
	v := outputView{decodedWindow: decodedWindow{bounds: []int{bounds[0]}, consumed: bounds[0], err: d.err}}
	var include, exclude *regexp.Regexp
	if c.IncludePattern != "" {
		include = regexp.MustCompile(c.IncludePattern)
	}
	if c.ExcludePattern != "" {
		exclude = regexp.MustCompile(c.ExcludePattern)
	}
	filtered := include != nil || exclude != nil
	var out strings.Builder
	for pos := 0; pos < len(text); {
		end := pos
		if filtered {
			if n := strings.IndexByte(text[pos:], '\n'); n >= 0 {
				end = pos + n + 1
			} else {
				end = len(text)
				if !final && end-pos < ViewLineLimit {
					break
				}
			}
			if end-pos > ViewLineLimit {
				end = pos + ViewLineLimit
				for end > pos && !utf8.RuneStart(text[end]) {
					end--
				}
				for end < len(text) && bounds[end+1] == bounds[end] {
					end++
				}
				v.fragments = true
			}
			line := text[pos:end]
			keep := (include == nil || include.MatchString(line)) && (exclude == nil || !exclude.MatchString(line))
			if !keep {
				v.filteredLines++
				v.filteredBytes += int64(end - pos)
				v.consumed = bounds[end]
				pos = end
				continue
			}
		} else {
			_, width := utf8.DecodeRuneInString(text[pos:])
			end = pos + width
			// Preserve a rendered invalid-byte escape as one indivisible unit.
			for end < len(text) && bounds[end+1] == bounds[end] {
				end++
			}
		}
		if int64(out.Len()+end-pos) > c.MaxOutputBytes {
			v.limited = true
			if out.Len() == 0 {
				v.minimum = end - pos
			}
			break
		}
		out.WriteString(text[pos:end])
		v.consumed = bounds[end]
		pos = end
	}
	for _, removal := range stripped {
		if removal.end <= v.consumed {
			v.strippedBytes += removal.bytes
		}
	}
	v.text = out.String()
	v.incomplete = v.consumed < d.consumed && !v.limited
	return v
}
