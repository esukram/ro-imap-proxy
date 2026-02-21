package imap

import (
	"testing"
)

func TestParseLiteral(t *testing.T) {
	tests := []struct {
		name        string
		input       []byte
		wantN       int64
		wantNonSync bool
		wantOk      bool
	}{
		{
			name:        "synchronizing literal",
			input:       []byte("A003 APPEND INBOX {26}\r\n"),
			wantN:       26,
			wantNonSync: false,
			wantOk:      true,
		},
		{
			name:        "non-synchronizing literal (LITERAL+)",
			input:       []byte("A003 APPEND INBOX {26+}\r\n"),
			wantN:       26,
			wantNonSync: true,
			wantOk:      true,
		},
		{
			name:   "no literal",
			input:  []byte("A001 SELECT INBOX\r\n"),
			wantOk: false,
		},
		{
			name:        "literal zero bytes",
			input:       []byte("A001 APPEND INBOX {0}\r\n"),
			wantN:       0,
			wantNonSync: false,
			wantOk:      true,
		},
		{
			name:        "large literal",
			input:       []byte("A001 APPEND INBOX {1048576}\r\n"),
			wantN:       1048576,
			wantNonSync: false,
			wantOk:      true,
		},
		{
			name:   "empty braces",
			input:  []byte("A001 APPEND INBOX {}\r\n"),
			wantOk: false,
		},
		{
			name:   "non-numeric content",
			input:  []byte("A001 APPEND INBOX {abc}\r\n"),
			wantOk: false,
		},
		{
			name:   "only plus in braces",
			input:  []byte("A001 APPEND INBOX {+}\r\n"),
			wantOk: false,
		},
		{
			name:   "empty line",
			input:  []byte(""),
			wantOk: false,
		},
		{
			name:   "only CRLF",
			input:  []byte("\r\n"),
			wantOk: false,
		},
		{
			name:   "no closing brace",
			input:  []byte("A001 APPEND INBOX {26\r\n"),
			wantOk: false,
		},
		{
			name:   "no opening brace",
			input:  []byte("A001 APPEND INBOX 26}\r\n"),
			wantOk: false,
		},
		{
			name:        "literal without CRLF",
			input:       []byte("A001 APPEND INBOX {5}"),
			wantN:       5,
			wantNonSync: false,
			wantOk:      true,
		},
		{
			name:        "literal with only LF",
			input:       []byte("A001 APPEND INBOX {5}\n"),
			wantN:       5,
			wantNonSync: false,
			wantOk:      true,
		},
		{
			name:   "negative number",
			input:  []byte("A001 APPEND INBOX {-1}\r\n"),
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, nonSync, ok := ParseLiteral(tt.input)
			if ok != tt.wantOk {
				t.Fatalf("ok: got %v, want %v", ok, tt.wantOk)
			}
			if !ok {
				return
			}
			if n != tt.wantN {
				t.Errorf("n: got %d, want %d", n, tt.wantN)
			}
			if nonSync != tt.wantNonSync {
				t.Errorf("nonSync: got %v, want %v", nonSync, tt.wantNonSync)
			}
		})
	}
}
