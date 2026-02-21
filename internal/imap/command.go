package imap

import (
	"bytes"
	"errors"
	"strings"
)

// Command represents a parsed IMAP command line.
type Command struct {
	Tag     string // e.g. "A001"
	Verb    string // uppercased, e.g. "SELECT", "UID"
	SubVerb string // for UID commands: "FETCH", "STORE", etc.
	Raw     []byte // original line including CRLF
}

var (
	errEmptyLine   = errors.New("empty line")
	errMissingTag  = errors.New("missing tag")
	errMissingVerb = errors.New("missing verb")
)

// ParseCommand parses an IMAP command line into a Command.
// The line should include the trailing CRLF.
func ParseCommand(line []byte) (Command, error) {
	if len(line) == 0 {
		return Command{}, errEmptyLine
	}

	// Work on a copy without CRLF for parsing, but preserve Raw.
	raw := make([]byte, len(line))
	copy(raw, line)

	data := line
	// Strip trailing CRLF or LF.
	data = bytes.TrimRight(data, "\r\n")

	if len(data) == 0 {
		return Command{}, errEmptyLine
	}

	// Find first SP → tag.
	spIdx := bytes.IndexByte(data, ' ')
	if spIdx < 0 {
		// No space: the whole thing is a tag with no verb.
		// Treat the token as a verb-less command (e.g. bare "DONE").
		// Per spec DONE has no tag, handle gracefully.
		token := strings.ToUpper(string(data))
		if token == "DONE" {
			return Command{Tag: "", Verb: "DONE", Raw: raw}, nil
		}
		// Otherwise: we have a tag but no verb.
		return Command{}, errMissingVerb
	}

	tag := string(data[:spIdx])
	if tag == "" {
		return Command{}, errMissingTag
	}

	rest := data[spIdx+1:]
	if len(rest) == 0 {
		return Command{}, errMissingVerb
	}

	// Find next SP → verb.
	sp2 := bytes.IndexByte(rest, ' ')
	var verb string
	var afterVerb []byte
	if sp2 < 0 {
		verb = string(rest)
		afterVerb = nil
	} else {
		verb = string(rest[:sp2])
		afterVerb = rest[sp2+1:]
	}

	verb = strings.ToUpper(verb)
	if verb == "" {
		return Command{}, errMissingVerb
	}

	cmd := Command{
		Tag:  tag,
		Verb: verb,
		Raw:  raw,
	}

	// If verb is UID, extract subverb from next token.
	if verb == "UID" && len(afterVerb) > 0 {
		sp3 := bytes.IndexByte(afterVerb, ' ')
		var subVerb string
		if sp3 < 0 {
			subVerb = string(afterVerb)
		} else {
			subVerb = string(afterVerb[:sp3])
		}
		cmd.SubVerb = strings.ToUpper(subVerb)
	}

	return cmd, nil
}
