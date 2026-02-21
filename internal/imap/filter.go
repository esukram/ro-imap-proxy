package imap


// Action describes what the filter decided to do with a command.
type Action int

const (
	Allow   Action = iota
	Block
	Rewrite
)

// FilterResult holds the filter decision for a command.
type FilterResult struct {
	Action    Action
	Rewritten []byte // only set when Action == Rewrite
	RejectMsg string // only set when Action == Block
}

// blockedVerbs lists IMAP verbs that mutate mailbox state.
var blockedVerbs = map[string]bool{
	"STORE":          true,
	"COPY":           true,
	"MOVE":           true,
	"DELETE":         true,
	"EXPUNGE":        true,
	"APPEND":         true,
	"CREATE":         true,
	"RENAME":         true,
	"SUBSCRIBE":      true,
	"UNSUBSCRIBE":    true,
	"AUTHENTICATE":   true,
}

// blockedUIDSubVerbs lists UID sub-commands that mutate mailbox state.
var blockedUIDSubVerbs = map[string]bool{
	"STORE":   true,
	"COPY":    true,
	"MOVE":    true,
	"EXPUNGE": true,
}

// Filter decides whether to allow, block, or rewrite an IMAP command.
func Filter(cmd Command) FilterResult {
	if cmd.Verb == "UID" {
		if blockedUIDSubVerbs[cmd.SubVerb] {
			return FilterResult{
				Action:    Block,
				RejectMsg: cmd.Tag + " NO UID subcommand not allowed in read-only mode\r\n",
			}
		}
		return FilterResult{Action: Allow}
	}

	if blockedVerbs[cmd.Verb] {
		return FilterResult{
			Action:    Block,
			RejectMsg: cmd.Tag + " NO " + cmd.Verb + " not allowed in read-only mode\r\n",
		}
	}

	if cmd.Verb == "SELECT" {
		// Replace the verb positionally to handle case-insensitive matching.
		// The verb starts right after "tag " in the raw line.
		verbStart := len(cmd.Tag) + 1
		verbEnd := verbStart + len("SELECT")
		rewritten := make([]byte, 0, len(cmd.Raw)+1) // EXAMINE is 1 char longer
		rewritten = append(rewritten, cmd.Raw[:verbStart]...)
		rewritten = append(rewritten, []byte("EXAMINE")...)
		rewritten = append(rewritten, cmd.Raw[verbEnd:]...)
		return FilterResult{
			Action:    Rewrite,
			Rewritten: rewritten,
		}
	}

	return FilterResult{Action: Allow}
}
