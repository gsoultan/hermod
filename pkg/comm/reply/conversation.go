package reply

import "github.com/google/uuid"

// A conversation id ties the messages of one chat together, so a workflow can
// remember what was said earlier in it. A caller sends it with each message
// (HeaderConversationID) and the transport puts it in the message's metadata
// (MetaConversationID); a synchronous caller that sent none is given one in the
// answer, to send with its next message.
const (
	HeaderConversationID = "X-Conversation-Id"
	MetaConversationID   = "conversation_id"

	maxConversationIDLen = 128
)

// ValidConversationID reports whether id is 1 to 128 characters of letters,
// digits, '.', '_', ':' and '-'. The id comes from the caller and ends up in
// storage keys and response headers, so anything else is refused rather than
// escaped.
func ValidConversationID(id string) bool {
	if id == "" || len(id) > maxConversationIDLen {
		return false
	}
	for i := range len(id) {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == ':', c == '-':
		default:
			return false
		}
	}
	return true
}

// NewConversationID starts a conversation.
func NewConversationID() string {
	return uuid.NewString()
}
