package imap

import (
	"bytes"
	"strconv"
)

// ParseLiteral scans the line (which should include CRLF) for an IMAP
// literal specification of the form {N} or {N+} at the end.
// It returns the literal byte count n, whether it is non-synchronizing
// (LITERAL+), and ok=true if a literal was found.
func ParseLiteral(line []byte) (n int64, nonSync bool, ok bool) {
	// Strip trailing CRLF.
	data := bytes.TrimRight(line, "\r\n")
	if len(data) == 0 {
		return 0, false, false
	}

	// Must end with '}'.
	if data[len(data)-1] != '}' {
		return 0, false, false
	}

	// Scan backwards for '{'.
	closeIdx := len(data) - 1
	openIdx := bytes.LastIndexByte(data[:closeIdx], '{')
	if openIdx < 0 {
		return 0, false, false
	}

	// Content between '{' and '}'.
	inner := data[openIdx+1 : closeIdx]
	if len(inner) == 0 {
		return 0, false, false
	}

	// Check for '+' suffix (non-synchronizing LITERAL+).
	ns := false
	if inner[len(inner)-1] == '+' {
		ns = true
		inner = inner[:len(inner)-1]
	}

	if len(inner) == 0 {
		return 0, false, false
	}

	count, err := strconv.ParseInt(string(inner), 10, 64)
	if err != nil || count < 0 {
		return 0, false, false
	}

	return count, ns, true
}
