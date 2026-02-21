package imap

import (
	"bytes"
	"testing"
)

func TestFilter(t *testing.T) {
	tests := []struct {
		name           string
		cmd            Command
		wantAction     Action
		wantRejectMsg  string
		wantRewritten  []byte
	}{
		// Blocked verbs
		{
			name:          "block STORE",
			cmd:           Command{Tag: "A001", Verb: "STORE", Raw: []byte("A001 STORE 1 FLAGS (\\Seen)\r\n")},
			wantAction:    Block,
			wantRejectMsg: "A001 NO STORE not allowed in read-only mode\r\n",
		},
		{
			name:          "block COPY",
			cmd:           Command{Tag: "A002", Verb: "COPY", Raw: []byte("A002 COPY 1 Trash\r\n")},
			wantAction:    Block,
			wantRejectMsg: "A002 NO COPY not allowed in read-only mode\r\n",
		},
		{
			name:          "block MOVE",
			cmd:           Command{Tag: "A003", Verb: "MOVE", Raw: []byte("A003 MOVE 1 Trash\r\n")},
			wantAction:    Block,
			wantRejectMsg: "A003 NO MOVE not allowed in read-only mode\r\n",
		},
		{
			name:          "block DELETE",
			cmd:           Command{Tag: "A004", Verb: "DELETE", Raw: []byte("A004 DELETE mymailbox\r\n")},
			wantAction:    Block,
			wantRejectMsg: "A004 NO DELETE not allowed in read-only mode\r\n",
		},
		{
			name:          "block EXPUNGE",
			cmd:           Command{Tag: "A005", Verb: "EXPUNGE", Raw: []byte("A005 EXPUNGE\r\n")},
			wantAction:    Block,
			wantRejectMsg: "A005 NO EXPUNGE not allowed in read-only mode\r\n",
		},
		{
			name:          "block APPEND",
			cmd:           Command{Tag: "A006", Verb: "APPEND", Raw: []byte("A006 APPEND INBOX {10}\r\n")},
			wantAction:    Block,
			wantRejectMsg: "A006 NO APPEND not allowed in read-only mode\r\n",
		},
		{
			name:          "block CREATE",
			cmd:           Command{Tag: "A007", Verb: "CREATE", Raw: []byte("A007 CREATE newfolder\r\n")},
			wantAction:    Block,
			wantRejectMsg: "A007 NO CREATE not allowed in read-only mode\r\n",
		},
		{
			name:          "block RENAME",
			cmd:           Command{Tag: "A008", Verb: "RENAME", Raw: []byte("A008 RENAME oldfolder newfolder\r\n")},
			wantAction:    Block,
			wantRejectMsg: "A008 NO RENAME not allowed in read-only mode\r\n",
		},
		{
			name:          "block SUBSCRIBE",
			cmd:           Command{Tag: "A009", Verb: "SUBSCRIBE", Raw: []byte("A009 SUBSCRIBE INBOX\r\n")},
			wantAction:    Block,
			wantRejectMsg: "A009 NO SUBSCRIBE not allowed in read-only mode\r\n",
		},
		{
			name:          "block UNSUBSCRIBE",
			cmd:           Command{Tag: "A010", Verb: "UNSUBSCRIBE", Raw: []byte("A010 UNSUBSCRIBE INBOX\r\n")},
			wantAction:    Block,
			wantRejectMsg: "A010 NO UNSUBSCRIBE not allowed in read-only mode\r\n",
		},
		{
			name:          "block AUTHENTICATE",
			cmd:           Command{Tag: "A011", Verb: "AUTHENTICATE", Raw: []byte("A011 AUTHENTICATE PLAIN\r\n")},
			wantAction:    Block,
			wantRejectMsg: "A011 NO AUTHENTICATE not allowed in read-only mode\r\n",
		},

		// Blocked UID subverbs
		{
			name:          "block UID STORE",
			cmd:           Command{Tag: "B001", Verb: "UID", SubVerb: "STORE", Raw: []byte("B001 UID STORE 1:* FLAGS (\\Seen)\r\n")},
			wantAction:    Block,
			wantRejectMsg: "B001 NO UID subcommand not allowed in read-only mode\r\n",
		},
		{
			name:          "block UID COPY",
			cmd:           Command{Tag: "B002", Verb: "UID", SubVerb: "COPY", Raw: []byte("B002 UID COPY 1:* Trash\r\n")},
			wantAction:    Block,
			wantRejectMsg: "B002 NO UID subcommand not allowed in read-only mode\r\n",
		},
		{
			name:          "block UID MOVE",
			cmd:           Command{Tag: "B003", Verb: "UID", SubVerb: "MOVE", Raw: []byte("B003 UID MOVE 1:* Trash\r\n")},
			wantAction:    Block,
			wantRejectMsg: "B003 NO UID subcommand not allowed in read-only mode\r\n",
		},
		{
			name:          "block UID EXPUNGE",
			cmd:           Command{Tag: "B004", Verb: "UID", SubVerb: "EXPUNGE", Raw: []byte("B004 UID EXPUNGE 1:*\r\n")},
			wantAction:    Block,
			wantRejectMsg: "B004 NO UID subcommand not allowed in read-only mode\r\n",
		},

		// SELECT → EXAMINE rewrite
		{
			name:          "rewrite SELECT to EXAMINE",
			cmd:           Command{Tag: "C001", Verb: "SELECT", Raw: []byte("C001 SELECT INBOX\r\n")},
			wantAction:    Rewrite,
			wantRewritten: []byte("C001 EXAMINE INBOX\r\n"),
		},
		{
			name:          "rewrite SELECT with quoted mailbox",
			cmd:           Command{Tag: "C002", Verb: "SELECT", Raw: []byte("C002 SELECT \"My Folder\"\r\n")},
			wantAction:    Rewrite,
			wantRewritten: []byte("C002 EXAMINE \"My Folder\"\r\n"),
		},

		{
			name:          "rewrite lowercase select to EXAMINE",
			cmd:           Command{Tag: "C003", Verb: "SELECT", Raw: []byte("C003 select INBOX\r\n")},
			wantAction:    Rewrite,
			wantRewritten: []byte("C003 EXAMINE INBOX\r\n"),
		},
		{
			name:          "rewrite mixed case Select to EXAMINE",
			cmd:           Command{Tag: "C004", Verb: "SELECT", Raw: []byte("C004 Select INBOX\r\n")},
			wantAction:    Rewrite,
			wantRewritten: []byte("C004 EXAMINE INBOX\r\n"),
		},

		// Allowed commands
		{
			name:       "allow FETCH",
			cmd:        Command{Tag: "D001", Verb: "FETCH", Raw: []byte("D001 FETCH 1:* (FLAGS)\r\n")},
			wantAction: Allow,
		},
		{
			name:       "allow LIST",
			cmd:        Command{Tag: "D002", Verb: "LIST", Raw: []byte("D002 LIST \"\" *\r\n")},
			wantAction: Allow,
		},
		{
			name:       "allow LSUB",
			cmd:        Command{Tag: "D003", Verb: "LSUB", Raw: []byte("D003 LSUB \"\" *\r\n")},
			wantAction: Allow,
		},
		{
			name:       "allow STATUS",
			cmd:        Command{Tag: "D004", Verb: "STATUS", Raw: []byte("D004 STATUS INBOX (MESSAGES)\r\n")},
			wantAction: Allow,
		},
		{
			name:       "allow SEARCH",
			cmd:        Command{Tag: "D005", Verb: "SEARCH", Raw: []byte("D005 SEARCH ALL\r\n")},
			wantAction: Allow,
		},
		{
			name:       "allow NOOP",
			cmd:        Command{Tag: "D006", Verb: "NOOP", Raw: []byte("D006 NOOP\r\n")},
			wantAction: Allow,
		},
		{
			name:       "allow IDLE",
			cmd:        Command{Tag: "D007", Verb: "IDLE", Raw: []byte("D007 IDLE\r\n")},
			wantAction: Allow,
		},
		{
			name:       "allow LOGOUT",
			cmd:        Command{Tag: "D008", Verb: "LOGOUT", Raw: []byte("D008 LOGOUT\r\n")},
			wantAction: Allow,
		},
		{
			name:       "allow CAPABILITY",
			cmd:        Command{Tag: "D009", Verb: "CAPABILITY", Raw: []byte("D009 CAPABILITY\r\n")},
			wantAction: Allow,
		},
		{
			name:       "allow CHECK",
			cmd:        Command{Tag: "D010", Verb: "CHECK", Raw: []byte("D010 CHECK\r\n")},
			wantAction: Allow,
		},
		{
			name:       "allow CLOSE",
			cmd:        Command{Tag: "D011", Verb: "CLOSE", Raw: []byte("D011 CLOSE\r\n")},
			wantAction: Allow,
		},
		{
			name:       "allow EXAMINE (direct)",
			cmd:        Command{Tag: "D012", Verb: "EXAMINE", Raw: []byte("D012 EXAMINE INBOX\r\n")},
			wantAction: Allow,
		},
		{
			name:       "allow UID FETCH",
			cmd:        Command{Tag: "D013", Verb: "UID", SubVerb: "FETCH", Raw: []byte("D013 UID FETCH 1:* (FLAGS)\r\n")},
			wantAction: Allow,
		},
		{
			name:       "allow UID SEARCH",
			cmd:        Command{Tag: "D014", Verb: "UID", SubVerb: "SEARCH", Raw: []byte("D014 UID SEARCH ALL\r\n")},
			wantAction: Allow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Filter(tt.cmd)

			if result.Action != tt.wantAction {
				t.Errorf("Action = %d, want %d", result.Action, tt.wantAction)
			}

			if tt.wantAction == Block {
				if result.RejectMsg != tt.wantRejectMsg {
					t.Errorf("RejectMsg = %q, want %q", result.RejectMsg, tt.wantRejectMsg)
				}
			}

			if tt.wantAction == Rewrite {
				if !bytes.Equal(result.Rewritten, tt.wantRewritten) {
					t.Errorf("Rewritten = %q, want %q", result.Rewritten, tt.wantRewritten)
				}
			}
		})
	}
}
