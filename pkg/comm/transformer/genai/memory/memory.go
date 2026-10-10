// Package memory gives an AI node a memory of the conversation a message
// belongs to: the earlier exchanges of the same chat, loaded before the model
// is called and extended after it answers.
//
// A conversation is named by an id the message carries (a webhook in reply
// mode puts X-Conversation-Id in the metadata as conversation_id). Turns are
// kept in the engine's state store under a key scoped to the vhost, workflow
// and node, so two workflows, or two nodes of one, never share a memory. What
// is kept is bounded three ways: a number of exchanges, a size per turn and in
// total, and an age after which the conversation is forgotten.
package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/reply"
)

// ConfigKey is the node config key holding the memory options:
// {"conversationField": "conversation_id", "maxTurns": 10, "ttl": "24h"}.
const ConfigKey = "memory"

const (
	// DefaultField is where the conversation id is looked for: in the payload
	// first, then the metadata.
	DefaultField = reply.MetaConversationID
	// DefaultMaxTurns is how many exchanges (a user turn and its answer) are
	// remembered when the node does not say.
	DefaultMaxTurns = 10
	// MaxTurnsLimit caps maxTurns: every remembered turn is sent with every call.
	MaxTurnsLimit = 50
	// DefaultTTL is how long a quiet conversation is remembered.
	DefaultTTL = 24 * time.Hour
	// MaxTTL caps ttl.
	MaxTTL = 30 * 24 * time.Hour
	// MaxTurnBytes caps one remembered turn; a longer one is cut.
	MaxTurnBytes = 4 << 10
	// MaxTotalBytes caps the text of one conversation; the oldest exchanges go
	// first.
	MaxTotalBytes = 64 << 10
)

// Roles of a turn.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Config is a node's memory options.
type Config struct {
	Field    string
	MaxTurns int
	TTL      time.Duration
}

// Parse reads a node's memory options. on is false when the node has none or
// sets enabled to false.
func Parse(raw any) (cfg Config, on bool, err error) {
	if raw == nil {
		return Config{}, false, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return Config{}, false, errors.New("memory must be an object")
	}
	if enabled, set := m["enabled"].(bool); set && !enabled {
		return Config{}, false, nil
	}
	cfg = Config{Field: DefaultField, MaxTurns: DefaultMaxTurns, TTL: DefaultTTL}
	if err := cfg.read(m); err != nil {
		return Config{}, false, err
	}
	return cfg, true, nil
}

// read overrides the defaults with the options m sets.
func (cfg *Config) read(m map[string]any) error {
	if v, set := m["conversationField"]; set {
		s, isString := v.(string)
		if !isString {
			return errors.New("memory.conversationField must be a string")
		}
		if s != "" {
			cfg.Field = s
		}
	}
	if v, set := m["maxTurns"]; set {
		n, err := wholeNumber(v)
		if err != nil || n < 1 || n > MaxTurnsLimit {
			return fmt.Errorf("memory.maxTurns must be a whole number from 1 to %d", MaxTurnsLimit)
		}
		cfg.MaxTurns = n
	}
	if v, set := m["ttl"]; set {
		s, _ := v.(string)
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 || d > MaxTTL {
			return fmt.Errorf("memory.ttl must be a duration such as \"24h\", at most %s", MaxTTL)
		}
		cfg.TTL = d
	}
	return nil
}

func wholeNumber(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case float64:
		if n != math.Trunc(n) || n > math.MaxInt32 || n < math.MinInt32 {
			return 0, errors.New("not a whole number")
		}
		return int(n), nil
	default:
		return 0, errors.New("not a number")
	}
}

// Turn is one remembered message of a conversation.
type Turn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// record is what is stored. The store may not expire keys itself, so the
// expiry travels with the value.
type record struct {
	Turns   []Turn    `json:"turns"`
	Expires time.Time `json:"expires"`
}

var (
	nowMu sync.RWMutex
	nowFn = time.Now
)

func now() time.Time {
	nowMu.RLock()
	defer nowMu.RUnlock()
	return nowFn()
}

// setNow is for tests.
func setNow(f func() time.Time) (restore func()) {
	nowMu.Lock()
	defer nowMu.Unlock()
	old := nowFn
	nowFn = f
	return func() {
		nowMu.Lock()
		defer nowMu.Unlock()
		nowFn = old
	}
}

// Conversation is one chat's memory at one node.
type Conversation struct {
	store hermod.StateStore
	key   string
	cfg   Config
}

// Open finds the memory of the conversation msg belongs to. It fails when the
// engine has no state store, or msg names no valid conversation: a node told
// to remember must not quietly answer as if the chat had just begun.
func Open(ctx context.Context, msg hermod.Message, cfg Config) (*Conversation, error) {
	store, ok := ctx.Value(hermod.StateStoreKey).(hermod.StateStore)
	if !ok || store == nil {
		return nil, errors.New("conversation memory needs a state store, and none is configured")
	}
	id := conversationID(msg, cfg.Field)
	if id == "" {
		return nil, fmt.Errorf("conversation memory: the message has no %q", cfg.Field)
	}
	if !reply.ValidConversationID(id) {
		return nil, fmt.Errorf("conversation memory: %q is not a valid conversation id", cfg.Field)
	}
	vhost := ""
	if s, scoped := msg.(hermod.VHostScoped); scoped {
		vhost = s.VHost()
	}
	workflowID, _ := hermod.MetadataValue(msg, "_hermod_workflow_id")
	nodeID, _ := ctx.Value(hermod.NodeIDKey).(string)
	// The id is the caller's; hashing it keeps the key's shape fixed whatever
	// the caller sends.
	sum := sha256.Sum256([]byte(id))
	key := fmt.Sprintf("ai_memory:%s:%s:%s:%s", vhost, workflowID, nodeID, hex.EncodeToString(sum[:16]))
	return &Conversation{store: store, key: key, cfg: cfg}, nil
}

func conversationID(msg hermod.Message, field string) string {
	if v, ok := msg.Data()[field]; ok {
		if s, isString := v.(string); isString && s != "" {
			return s
		}
	}
	v, _ := hermod.MetadataValue(msg, field)
	return v
}

// History is the remembered turns, oldest first, starting with a user turn.
func (c *Conversation) History(ctx context.Context) ([]Turn, error) {
	rec, err := c.load(ctx)
	return rec.Turns, err
}

// Append remembers one exchange and trims the conversation to its bounds.
func (c *Conversation) Append(ctx context.Context, user, assistant string) error {
	mu := lockFor(c.key)
	mu.Lock()
	defer mu.Unlock()
	// Reloaded under the lock: another message of the same chat may have
	// answered since this one read its history.
	rec, err := c.load(ctx)
	if err != nil {
		return err
	}
	rec.Turns = append(rec.Turns, Turn{RoleUser, cut(user)}, Turn{RoleAssistant, cut(assistant)})
	rec.Turns = trim(rec.Turns, c.cfg.MaxTurns)
	rec.Expires = now().Add(c.cfg.TTL)
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return c.store.Set(ctx, c.key, raw)
}

func (c *Conversation) load(ctx context.Context) (record, error) {
	raw, err := c.store.Get(ctx, c.key)
	if err != nil {
		return record{}, fmt.Errorf("conversation memory: %w", err)
	}
	if len(raw) == 0 {
		return record{}, nil
	}
	return live(raw), nil
}

// live decodes a stored conversation. One that is unreadable or expired is
// forgotten: it reads as empty, and the next Append overwrites it.
func live(raw []byte) record {
	var rec record
	if json.Unmarshal(raw, &rec) != nil || !now().Before(rec.Expires) {
		return record{}
	}
	return rec
}

// trim keeps at most maxTurns exchanges and MaxTotalBytes of text, dropping
// the oldest, and never starts on an answer.
func trim(turns []Turn, maxTurns int) []Turn {
	if limit := 2 * maxTurns; len(turns) > limit {
		turns = turns[len(turns)-limit:]
	}
	total := 0
	for _, t := range turns {
		total += len(t.Text)
	}
	for total > MaxTotalBytes && len(turns) > 0 {
		total -= len(turns[0].Text)
		turns = turns[1:]
	}
	for len(turns) > 0 && turns[0].Role != RoleUser {
		turns = turns[1:]
	}
	return turns
}

// cut bounds one turn, on a rune boundary.
func cut(s string) string {
	if len(s) <= MaxTurnBytes {
		return s
	}
	s = s[:MaxTurnBytes]
	// At most three trailing bytes of a split rune to drop.
	for i := 0; i < utf8.UTFMax && !utf8.ValidString(s); i++ {
		s = s[:len(s)-1]
	}
	return s
}

// stripes serialises appends to one conversation within this process. The
// state store has no compare-and-set, so two instances answering the same chat
// at once can still lose a turn: last writer wins.
var stripes [64]sync.Mutex

func lockFor(key string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &stripes[h.Sum32()%uint32(len(stripes))]
}
