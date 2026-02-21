package imap

import "testing"

func TestParseListResponse(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		want   string
		wantOK bool
	}{
		{
			name:   "LIST with flags and quoted mailbox",
			line:   "* LIST (\\HasNoChildren) \"/\" \"INBOX\"\r\n",
			want:   "INBOX",
			wantOK: true,
		},
		{
			name:   "LIST with nested folder",
			line:   "* LIST () \"/\" \"Archive/2024\"\r\n",
			want:   "Archive/2024",
			wantOK: true,
		},
		{
			name:   "LSUB response",
			line:   "* LSUB () \"/\" \"Sent\"\r\n",
			want:   "Sent",
			wantOK: true,
		},
		{
			name:   "LIST with Noselect and empty mailbox",
			line:   "* LIST (\\Noselect) \"/\" \"\"\r\n",
			want:   "",
			wantOK: true,
		},
		{
			name:   "atom mailbox unquoted",
			line:   "* LIST () \"/\" INBOX\r\n",
			want:   "INBOX",
			wantOK: true,
		},
		{
			name:   "NIL delimiter",
			line:   "* LIST () NIL INBOX\r\n",
			want:   "INBOX",
			wantOK: true,
		},
		{
			name:   "case-insensitive verb list",
			line:   "* list () \"/\" \"INBOX\"\r\n",
			want:   "INBOX",
			wantOK: true,
		},
		{
			name:   "case-insensitive verb lsub",
			line:   "* Lsub () \"/\" \"INBOX\"\r\n",
			want:   "INBOX",
			wantOK: true,
		},
		{
			name:   "not a LIST response - OK",
			line:   "* OK completed\r\n",
			wantOK: false,
		},
		{
			name:   "not a LIST response - FETCH",
			line:   "* 1 FETCH (FLAGS (\\Seen))\r\n",
			wantOK: false,
		},
		{
			name:   "tagged response",
			line:   "A001 OK LIST completed\r\n",
			wantOK: false,
		},
		{
			name:   "empty line",
			line:   "\r\n",
			wantOK: false,
		},
		{
			name:   "quoted mailbox with escaped quote",
			line:   "* LIST () \"/\" \"folder\\\"name\"\r\n",
			want:   "folder\"name",
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseListResponse([]byte(tt.line))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("mailbox = %q, want %q", got, tt.want)
			}
		})
	}
}
