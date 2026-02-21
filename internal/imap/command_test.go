package imap

import (
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name      string
		input     []byte
		wantTag   string
		wantVerb  string
		wantSub   string
		wantErr   bool
	}{
		{
			name:     "normal SELECT",
			input:    []byte("A001 SELECT INBOX\r\n"),
			wantTag:  "A001",
			wantVerb: "SELECT",
		},
		{
			name:     "normal SELECT lowercase verb",
			input:    []byte("A001 select INBOX\r\n"),
			wantTag:  "A001",
			wantVerb: "SELECT",
		},
		{
			name:     "UID FETCH",
			input:    []byte("A002 UID FETCH 1:* FLAGS\r\n"),
			wantTag:  "A002",
			wantVerb: "UID",
			wantSub:  "FETCH",
		},
		{
			name:     "UID STORE",
			input:    []byte("A003 UID STORE 1 +FLAGS (\\Deleted)\r\n"),
			wantTag:  "A003",
			wantVerb: "UID",
			wantSub:  "STORE",
		},
		{
			name:     "UID with lowercase subverb",
			input:    []byte("A004 uid fetch 1:* FLAGS\r\n"),
			wantTag:  "A004",
			wantVerb: "UID",
			wantSub:  "FETCH",
		},
		{
			name:     "NOOP no args",
			input:    []byte("A003 NOOP\r\n"),
			wantTag:  "A003",
			wantVerb: "NOOP",
		},
		{
			name:     "NOOP no args no CRLF",
			input:    []byte("A003 NOOP"),
			wantTag:  "A003",
			wantVerb: "NOOP",
		},
		{
			name:     "LOGOUT",
			input:    []byte("A005 LOGOUT\r\n"),
			wantTag:  "A005",
			wantVerb: "LOGOUT",
		},
		{
			name:     "CAPABILITY",
			input:    []byte("1 CAPABILITY\r\n"),
			wantTag:  "1",
			wantVerb: "CAPABILITY",
		},
		{
			name:     "LOGIN with args",
			input:    []byte("a1 LOGIN user pass\r\n"),
			wantTag:  "a1",
			wantVerb: "LOGIN",
		},
		{
			name:     "APPEND with literal",
			input:    []byte("A006 APPEND INBOX {26}\r\n"),
			wantTag:  "A006",
			wantVerb: "APPEND",
		},
		{
			name:     "DONE tagless",
			input:    []byte("DONE\r\n"),
			wantTag:  "",
			wantVerb: "DONE",
		},
		{
			name:     "DONE without CRLF",
			input:    []byte("DONE"),
			wantTag:  "",
			wantVerb: "DONE",
		},
		{
			name:    "empty line",
			input:   []byte(""),
			wantErr: true,
		},
		{
			name:    "only CRLF",
			input:   []byte("\r\n"),
			wantErr: true,
		},
		{
			name:    "missing verb",
			input:   []byte("A001\r\n"),
			wantErr: true,
		},
		{
			name:    "tag with trailing space but no verb",
			input:   []byte("A001 \r\n"),
			wantErr: true,
		},
		{
			name:     "UID with no subverb",
			input:    []byte("A007 UID\r\n"),
			wantTag:  "A007",
			wantVerb: "UID",
			wantSub:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := ParseCommand(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got cmd=%+v", cmd)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cmd.Tag != tt.wantTag {
				t.Errorf("Tag: got %q, want %q", cmd.Tag, tt.wantTag)
			}
			if cmd.Verb != tt.wantVerb {
				t.Errorf("Verb: got %q, want %q", cmd.Verb, tt.wantVerb)
			}
			if cmd.SubVerb != tt.wantSub {
				t.Errorf("SubVerb: got %q, want %q", cmd.SubVerb, tt.wantSub)
			}
			if string(cmd.Raw) != string(tt.input) {
				t.Errorf("Raw: got %q, want %q", cmd.Raw, tt.input)
			}
		})
	}
}
