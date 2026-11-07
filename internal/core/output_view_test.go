package core

import (
	"strings"
	"testing"
)

func TestANSIProjectionSplitSequencesAndRawHistory(t *testing.T) {
	m, s, _ := localSession(t)
	settings := cfg(map[string]any{"strip_ansi": true})
	_, _ = s.Write([]byte("\x1b[3"))
	v := run(t, m, "read_available", Request{Session: s.id, Config: settings})
	if v.Output != "" || v.End != 0 {
		t.Fatal(v)
	}
	v = run(t, m, "read_history", Request{Session: s.id, Config: settings})
	if v.Output != "" || v.End != 0 {
		t.Fatal("history projection finalized a live partial ANSI sequence", v)
	}
	_, _ = s.Write([]byte("1mred\x1b[0mX\x1b]0;title"))
	v = run(t, m, "read_available", Request{Session: s.id, Config: settings})
	if v.Output != "redX" || v.StrippedBytes != 9 {
		t.Fatal(v)
	}
	boundary := v.End
	_, _ = s.Write([]byte("\x07Y"))
	v = run(t, m, "read_available", Request{Session: s.id, Config: settings})
	if v.Output != "Y" || v.Start != boundary || v.StrippedBytes == 0 {
		t.Fatal(v)
	}
	v = run(t, m, "read_history", Request{Session: s.id})
	if v.Output != "\x1b[31mred\x1b[0mX\x1b]0;title\x07Y" {
		t.Fatal(v)
	}
}

func TestLineFiltersAndRawEndPattern(t *testing.T) {
	m, s, _ := localSession(t)
	_, _ = s.Write([]byte("noise\n\x1b[31mERROR useful\x1b[0m\nERROR DEBUG drop\nREADY>after\n"))
	v := run(t, m, "read_until", Request{Session: s.id, Config: cfg(map[string]any{"strip_ansi": true, "include_pattern": "ERROR", "exclude_pattern": "DEBUG", "end_pattern": "READY>"})})
	if v.Output != "ERROR useful\n" || v.Reason != "match" || v.FilteredLines != 3 || v.StrippedBytes != 9 {
		t.Fatal(v)
	}
	v = run(t, m, "read_available", Request{Session: s.id})
	if v.Output != "after\n" {
		t.Fatal(v)
	}
}

func TestFilteredScanBudgetProgressAndOutputLimit(t *testing.T) {
	m, s, _ := localSession(t)
	_, _ = s.Write([]byte(strings.Repeat("noise\n", 20000) + "ERROR wanted\n"))
	settings := cfg(map[string]any{"include_pattern": "ERROR"})
	v := run(t, m, "read_available", Request{Session: s.id, Config: settings})
	if v.Reason != "scan_limit" || v.Output != "" || v.End <= v.Start || v.FilteredLines == 0 || !v.More || v.DroppedBytes != 0 {
		t.Fatal(v)
	}
	v = run(t, m, "read_available", Request{Session: s.id, Config: settings})
	if v.Output != "ERROR wanted\n" || v.More {
		t.Fatal(v)
	}
	_, _ = s.Write([]byte("ERROR line\n"))
	v = run(t, m, "read_available", Request{Session: s.id, Config: cfg(map[string]any{"include_pattern": "ERROR", "max_output_bytes": 2})})
	if v.Reason != "output_limit" || v.Output != "" || v.MinimumOutputBytes != 11 {
		t.Fatal(v)
	}
	v = run(t, m, "read_available", Request{Session: s.id, Config: settings})
	if v.Output != "ERROR line\n" {
		t.Fatal(v)
	}
}

func TestProjectionBoundedPartialLineAndLongANSI(t *testing.T) {
	m, s, _ := localSession(t)
	_, _ = s.Write([]byte("ERROR partial"))
	settings := cfg(map[string]any{"include_pattern": "ERROR"})
	v := run(t, m, "read_available", Request{Session: s.id, Config: settings})
	if v.Output != "" || v.End != 0 {
		t.Fatal(v)
	}
	_, _ = s.Write([]byte(" completed\n"))
	v = run(t, m, "read_available", Request{Session: s.id, Config: settings})
	if v.Output != "ERROR partial completed\n" {
		t.Fatal(v)
	}
	_, _ = s.Write([]byte("\x1b]" + strings.Repeat("x", ViewScanLimit+1)))
	v = run(t, m, "read_available", Request{Session: s.id, Config: cfg(map[string]any{"strip_ansi": true})})
	if v.Reason != "view_incomplete" || v.Output != "" || v.End != v.Start || !v.More {
		t.Fatal(v)
	}
	v = run(t, m, "read_available", Request{Session: s.id})
	if !strings.HasPrefix(v.Output, "\x1b]") {
		t.Fatal(v)
	}
}

func TestReadTimeoutPreservesSplitANSISequence(t *testing.T) {
	m, s, _ := localSession(t)
	_, _ = s.Write([]byte("\x1b[3"))
	v := run(t, m, "read_until", Request{Session: s.id, Config: cfg(map[string]any{"strip_ansi": true, "read_idle_timeout_ms": 0, "read_total_timeout_ms": 20})})
	if v.Reason != "total_timeout" || v.End != 0 {
		t.Fatal(v)
	}
	_, _ = s.Write([]byte("1mY"))
	v = run(t, m, "read_available", Request{Session: s.id, Config: cfg(map[string]any{"strip_ansi": true})})
	if v.Output != "Y" || v.StrippedBytes != 5 {
		t.Fatal(v)
	}
}

func TestEndPatternInsideANSIReportsIncompleteProjection(t *testing.T) {
	m, s, _ := localSession(t)
	_, _ = s.Write([]byte("\x1b[31mOK"))
	v := run(t, m, "read_until", Request{Session: s.id, Config: cfg(map[string]any{"strip_ansi": true, "end_pattern": "31"})})
	if v.Reason != "view_incomplete" || v.End != 0 {
		t.Fatal(v)
	}
	v = run(t, m, "read_available", Request{Session: s.id})
	if v.Output != "\x1b[31mOK" {
		t.Fatal(v)
	}
}
