package imap

import (
	"bytes"
	"strings"
)

// ParseListResponse extracts the mailbox name from an IMAP LIST or LSUB
// untagged response. It returns ok=false if the line is not a LIST/LSUB response.
func ParseListResponse(line []byte) (mailbox string, ok bool) {
	data := bytes.TrimRight(line, "\r\n")

	// Must start with "* "
	if len(data) < 7 || data[0] != '*' || data[1] != ' ' {
		return "", false
	}
	rest := data[2:]

	// Verb: LIST or LSUB (case-insensitive), followed by space.
	if len(rest) < 5 || rest[4] != ' ' {
		return "", false
	}
	verb := strings.ToUpper(string(rest[:4]))
	if verb != "LIST" && verb != "LSUB" {
		return "", false
	}
	rest = rest[5:]

	// Parenthesized flags.
	rest = bytes.TrimLeft(rest, " ")
	if len(rest) == 0 || rest[0] != '(' {
		return "", false
	}
	closeIdx := bytes.IndexByte(rest, ')')
	if closeIdx < 0 {
		return "", false
	}
	rest = rest[closeIdx+1:]

	// Delimiter: quoted string or NIL.
	rest = bytes.TrimLeft(rest, " ")
	if len(rest) == 0 {
		return "", false
	}
	if rest[0] == '"' {
		end := bytes.IndexByte(rest[1:], '"')
		if end < 0 {
			return "", false
		}
		rest = rest[end+2:]
	} else if len(rest) >= 3 && strings.EqualFold(string(rest[:3]), "NIL") {
		rest = rest[3:]
	} else {
		return "", false
	}

	// Mailbox name: quoted string or atom.
	rest = bytes.TrimLeft(rest, " ")
	if len(rest) == 0 {
		return "", false
	}
	if rest[0] == '"' {
		var b strings.Builder
		i := 1
		for i < len(rest) {
			if rest[i] == '\\' && i+1 < len(rest) && rest[i+1] == '"' {
				b.WriteByte('"')
				i += 2
				continue
			}
			if rest[i] == '"' {
				return b.String(), true
			}
			b.WriteByte(rest[i])
			i++
		}
		return "", false
	}
	return string(rest), true
}
