package fcm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/hermod"
)

// DataMode decides what the FCM `data` map carries.
type DataMode string

const (
	// DataEnvelope puts the formatted message under `payload` alongside the
	// id/operation/table/schema envelope. It is the default because it is the
	// shape the sink has always sent, and apps already parse it.
	DataEnvelope DataMode = "envelope"
	// DataFields flattens the message's own fields into top-level data keys,
	// which is what a handset wants when it is going to read one or two of
	// them rather than re-parse a JSON blob.
	DataFields DataMode = "fields"
	// DataNone sends no data at all: a pure notification.
	DataNone DataMode = "none"
)

// OversizePolicy decides what happens to a message whose data map exceeds the
// FCM limit. FCM refuses such a message, and it will not get smaller on retry,
// so the choice is between failing it loudly and sending something smaller.
type OversizePolicy string

const (
	// OversizeError refuses the message with a permanent error. The default:
	// silently altering data of record is worse than a dead letter.
	OversizeError OversizePolicy = "error"
	// OversizeTruncate shortens the largest values until the map fits, marking
	// each one it cut. The envelope scalars are preserved, so the device can
	// still tell which row it is about and go fetch the rest.
	OversizeTruncate OversizePolicy = "truncate"
	// OversizeDrop sends the notification with no data map at all.
	OversizeDrop OversizePolicy = "drop"
)

// Action selects what the sink does with each message.
type Action string

const (
	// ActionSend delivers a message. The default.
	ActionSend Action = "send"
	// ActionSubscribe subscribes the resolved tokens to the resolved topic,
	// which is how a device-registration table becomes an FCM audience.
	ActionSubscribe Action = "subscribe"
	// ActionUnsubscribe is its inverse, for a de-registration or a delete.
	ActionUnsubscribe Action = "unsubscribe"
)

// defaultMaxDataBytes is FCM's own limit on the data map, in bytes. Anything
// larger is refused by the service.
const defaultMaxDataBytes = 4096

// maxMulticastTokens is FCM's per-call ceiling for a multicast send.
const maxMulticastTokens = 500

// maxTopicTokens is FCM's per-call ceiling for topic subscription.
const maxTopicTokens = 1000

// AndroidConfig is the Android half of an FCM message. Every field is
// optional; the templated ones are noted.
type AndroidConfig struct {
	// Priority is "high" or "normal". High wakes a dozing device.
	Priority string
	// TTL is how long FCM keeps trying. Zero leaves it to FCM's default (4 weeks).
	TTL time.Duration
	// CollapseKey replaces an undelivered message with the same key. Templated,
	// so one key per order rather than one per workflow.
	CollapseKey string
	// RestrictedPackageName limits delivery to one app package.
	RestrictedPackageName string

	// The rest configure the Android notification itself.
	ChannelID            string
	Sound                string
	Icon                 string
	Color                string // #rrggbb
	Tag                  string // templated
	ClickAction          string
	NotificationPriority string // min, low, default, high, max
}

// APNSConfig is the Apple half.
type APNSConfig struct {
	// Priority is "10" (immediate) or "5" (power-considerate). APNs refuses 10
	// for a background push, which is what ContentAvailable makes this.
	Priority string
	// Expiration is how long APNs stores an undelivered push.
	Expiration time.Duration
	// CollapseID is APNs' equivalent of a collapse key. Templated.
	CollapseID string

	Sound string
	// Badge is templated and must render to an integer.
	Badge string
	// ContentAvailable makes this a background push: no alert, the app is woken
	// to fetch. Pair it with an empty title and body.
	ContentAvailable bool
	// MutableContent lets a notification service extension rewrite the alert.
	MutableContent bool
	Category       string
	ThreadID       string // templated
}

// WebpushConfig is the browser half.
type WebpushConfig struct {
	Link  string // templated
	Icon  string
	Badge string
	TTL   time.Duration
}

// Config is everything the FCM sink needs. Fields marked templated are Go
// templates over the message — see renderData for what is in scope.
type Config struct {
	// CredentialsJSON is a Firebase service account key. It carries the
	// project, so ProjectID is only needed when this is empty.
	CredentialsJSON string
	// ProjectID overrides the project in the credentials, and is required when
	// UseDefaultCredentials is set.
	ProjectID string
	// UseDefaultCredentials opts in to Google Application Default Credentials.
	// It has to be explicit: an empty CredentialsJSON used to fall through to
	// ADC silently, so a machine with gcloud logged in pushed to whatever
	// project that account happened to default to.
	UseDefaultCredentials bool

	// Exactly one of Token, Topic and Condition may be set — that is FCM's own
	// rule, not ours. All three may be left empty, in which case every message
	// must name its destination through fcm_token/fcm_topic/fcm_condition
	// metadata. All are templated.
	//
	// Token may render to a comma-separated list, which fans out as a
	// multicast send; that is also how the subscribe actions take their
	// device list.
	Token     string
	Topic     string
	Condition string

	// Notification. Leaving Title and Body empty sends a data-only message,
	// which is what an app that renders its own notification wants.
	Title    string // templated
	Body     string // templated
	ImageURL string // templated

	DataMode DataMode
	// Data is merged over the top of whatever DataMode produced. Values are
	// templated; it is how a deep link gets into the payload.
	Data map[string]string
	// MaxDataBytes defaults to FCM's 4096.
	MaxDataBytes int
	OnOversize   OversizePolicy

	Android AndroidConfig
	APNS    APNSConfig
	Webpush WebpushConfig

	// AnalyticsLabel tags the send in Firebase analytics.
	AnalyticsLabel string
	// DryRun runs every send through FCM's validation without delivering it.
	DryRun bool
	// Timeout bounds a single call. Zero means no sink-imposed deadline.
	Timeout time.Duration

	Action Action

	// Formatter renders the message body carried under the `payload` data key
	// in DataEnvelope mode. Nil sends the raw payload.
	Formatter hermod.Formatter

	// Endpoint and HTTPClient replace the FCM endpoint and transport. They
	// exist for tests and for the Firebase emulator; leave both empty in
	// production.
	Endpoint   string
	HTTPClient *http.Client
}

// FromMap builds a Config from the flat string map a sink configuration
// carries. Every value that cannot be parsed is an error rather than a zero
// value: a mistyped duration that silently means "off" is how the trace purge
// stopped running for long enough to grow PostgreSQL by 50 GB.
func FromMap(m map[string]string) (Config, error) {
	cfg, _, err := fromMap(m)
	return cfg, err
}

// ConfigKeys is every key FromMap reads, in sorted order. It is derived from
// FromMap rather than restated beside it, so a key added there cannot go
// missing here.
func ConfigKeys() []string {
	_, seen, _ := fromMap(nil)
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func fromMap(m map[string]string) (Config, map[string]bool, error) {
	p := parser{m: m, seen: map[string]bool{}}

	cfg := Config{
		CredentialsJSON:       p.str("credentials_json"),
		ProjectID:             p.str("project_id"),
		UseDefaultCredentials: p.boolean("use_default_credentials"),

		Token:     p.str("device_token", "token"),
		Topic:     p.str("topic"),
		Condition: p.str("condition"),

		Title:    p.str("title"),
		Body:     p.str("body"),
		ImageURL: p.str("image_url"),

		DataMode:     DataMode(p.oneOf("data_mode", string(DataEnvelope), string(DataEnvelope), string(DataFields), string(DataNone))),
		Data:         p.jsonObject("data_json"),
		MaxDataBytes: p.integer("max_data_bytes"),
		OnOversize:   OversizePolicy(p.oneOf("on_oversize", string(OversizeError), string(OversizeError), string(OversizeTruncate), string(OversizeDrop))),

		Android: p.android(),
		APNS:    p.apns(),
		Webpush: p.webpush(),

		AnalyticsLabel: p.str("analytics_label"),
		DryRun:         p.boolean("dry_run"),
		Timeout:        p.duration("timeout"),
		Action:         Action(p.oneOf("action", string(ActionSend), string(ActionSend), string(ActionSubscribe), string(ActionUnsubscribe))),
	}

	if p.err != nil {
		return Config{}, p.seen, p.err
	}
	return cfg, p.seen, nil
}

func (p *parser) android() AndroidConfig {
	return AndroidConfig{
		Priority:              p.oneOf("android_priority", "", "", "high", "normal"),
		TTL:                   p.duration("android_ttl"),
		CollapseKey:           p.str("android_collapse_key"),
		RestrictedPackageName: p.str("android_restricted_package_name"),
		ChannelID:             p.str("android_channel_id"),
		Sound:                 p.str("android_sound"),
		Icon:                  p.str("android_icon"),
		Color:                 p.str("android_color"),
		Tag:                   p.str("android_tag"),
		ClickAction:           p.str("android_click_action"),
		NotificationPriority:  p.oneOf("android_notification_priority", "", "", "min", "low", "default", "high", "max"),
	}
}

func (p *parser) apns() APNSConfig {
	return APNSConfig{
		Priority:         p.oneOf("apns_priority", "", "", "5", "10"),
		Expiration:       p.duration("apns_expiration"),
		CollapseID:       p.str("apns_collapse_id"),
		Sound:            p.str("apns_sound"),
		Badge:            p.str("apns_badge"),
		ContentAvailable: p.boolean("apns_content_available"),
		MutableContent:   p.boolean("apns_mutable_content"),
		Category:         p.str("apns_category"),
		ThreadID:         p.str("apns_thread_id"),
	}
}

func (p *parser) webpush() WebpushConfig {
	return WebpushConfig{
		Link:  p.str("webpush_link"),
		Icon:  p.str("webpush_icon"),
		Badge: p.str("webpush_badge"),
		TTL:   p.duration("webpush_ttl"),
	}
}

// parser reads the configuration map, accumulating the first parse failure so
// FromMap can report it whole rather than each caller re-checking an error
// after every field.
//
// It also records every key it looks at. That record is what ConfigKeys
// returns, and what the editor's form is checked against: a field the form
// writes under a name nothing reads is invisible until someone reports that
// their setting does nothing.
type parser struct {
	m    map[string]string
	seen map[string]bool
	err  error
}

// str returns the first of keys that is present and non-blank, recording all
// of them as read.
func (p *parser) str(keys ...string) string {
	out := ""
	for _, k := range keys {
		p.seen[k] = true
		if out == "" {
			if v := strings.TrimSpace(p.m[k]); v != "" {
				out = v
			}
		}
	}
	return out
}

func (p *parser) fail(key, value, want string) {
	if p.err == nil {
		p.err = fmt.Errorf("fcm sink: %s = %q is not %s", key, value, want)
	}
}

func (p *parser) duration(key string) time.Duration {
	raw := p.str(key)
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		p.fail(key, raw, `a duration such as "30s", "10m" or "2h"`)
		return 0
	}
	if d < 0 {
		p.fail(key, raw, "a duration at or above zero")
	}
	return d
}

func (p *parser) integer(key string) int {
	raw := p.str(key)
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		p.fail(key, raw, "a whole number")
		return 0
	}
	return n
}

// boolean treats anything other than an explicit true as false, which is what
// a checkbox that has never been touched sends.
func (p *parser) boolean(key string) bool {
	raw := strings.ToLower(p.str(key))
	switch raw {
	case "", "false", "0", "no", "off":
		return false
	case "true", "1", "yes", "on":
		return true
	default:
		p.fail(key, raw, "true or false")
		return false
	}
}

func (p *parser) oneOf(key, fallback string, allowed ...string) string {
	raw := p.str(key)
	if raw == "" {
		return fallback
	}
	for _, a := range allowed {
		if raw == a {
			return raw
		}
	}
	p.fail(key, raw, "one of "+strings.Join(allowed, ", "))
	return fallback
}

func (p *parser) jsonObject(key string) map[string]string {
	raw := p.str(key)
	if raw == "" {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		p.fail(key, raw, "a JSON object of string values")
		return nil
	}
	return out
}
