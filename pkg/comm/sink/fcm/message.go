package fcm

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"firebase.google.com/go/v4/messaging"
	"github.com/gsoultan/hermod"
)

// Metadata keys a message may use to override the sink's configuration. The
// three destination keys predate this package's Config and stay honoured:
// workflows set them from a transformer.
const (
	metaToken      = "fcm_token"
	metaTopic      = "fcm_topic"
	metaCondition  = "fcm_condition"
	metaTitle      = "fcm_notification_title"
	metaBody       = "fcm_notification_body"
	metaImage      = "fcm_notification_image"
	metaCollapse   = "fcm_collapse_key"
	metaAnalytics  = "fcm_analytics_label"
	truncationMark = "…[truncated]"
)

// envelopeKeys are the data keys the sink adds itself. They are small, they
// identify the row, and truncation protects them: a device that receives a
// shortened payload can still tell what it is about and go and fetch the rest.
var envelopeKeys = []string{"id", "operation", "table", "schema"}

// target is where one message is going.
type target struct {
	// tokens is non-empty for a device-addressed send. More than one means a
	// multicast, and it is also the device list for the subscribe actions.
	tokens    []string
	topic     string
	condition string
}

// resolveTarget decides where a message goes.
//
// Metadata wins over configuration, because a workflow that sets fcm_token per
// row is being more specific than the sink's default. A metadata key that is
// present but blank does not count: a transformer copying a nullable column
// writes the key with an empty value, and treating that as a destination sent
// messages with no token at all rather than falling back.
func (s *Sink) resolveTarget(msg hermod.Message, data map[string]any) (target, error) {
	metadata := msg.Metadata()

	if v := strings.TrimSpace(metadata[metaToken]); v != "" {
		return target{tokens: splitList(v)}, nil
	}
	if v := strings.TrimSpace(metadata[metaTopic]); v != "" {
		return target{topic: v}, nil
	}
	if v := strings.TrimSpace(metadata[metaCondition]); v != "" {
		return target{condition: v}, nil
	}

	switch {
	case !s.token.empty():
		rendered, err := s.token.render(data)
		if err != nil {
			return target{}, err
		}
		tokens := splitList(rendered)
		if len(tokens) == 0 {
			return target{}, permanentf("fcm sink: the token template rendered nothing for message %s; there is no device to send to", msg.ID())
		}
		return target{tokens: tokens}, nil
	case !s.topic.empty():
		rendered, err := s.topic.render(data)
		if err != nil {
			return target{}, err
		}
		if rendered = strings.TrimSpace(rendered); rendered == "" {
			return target{}, permanentf("fcm sink: the topic template rendered nothing for message %s", msg.ID())
		}
		return target{topic: rendered}, nil
	case !s.condition.empty():
		rendered, err := s.condition.render(data)
		if err != nil {
			return target{}, err
		}
		if rendered = strings.TrimSpace(rendered); rendered == "" {
			return target{}, permanentf("fcm sink: the condition template rendered nothing for message %s", msg.ID())
		}
		return target{condition: rendered}, nil
	}

	return target{}, permanentf(
		"fcm sink: no fcm destination for message %s: the sink has no default token, topic or condition, and the message carries no %s, %s or %s metadata",
		msg.ID(), metaToken, metaTopic, metaCondition)
}

// build turns one hermod message into the FCM message (or messages, for a
// multicast) that carry it.
func (s *Sink) build(msg hermod.Message) ([]*messaging.Message, error) {
	data := renderData(msg)

	tgt, err := s.resolveTarget(msg, data)
	if err != nil {
		return nil, err
	}

	payload, err := s.dataMap(msg, data)
	if err != nil {
		return nil, err
	}

	notification, err := s.notification(msg, data)
	if err != nil {
		return nil, err
	}
	if notification == nil && len(payload) == 0 {
		return nil, permanentf("fcm sink: message %s would be empty: no notification and no data", msg.ID())
	}

	base := messaging.Message{
		Data:         payload,
		Notification: notification,
		FCMOptions:   s.fcmOptions(msg),
		Topic:        tgt.topic,
		Condition:    tgt.condition,
	}
	if err := s.applyPlatforms(&base, data); err != nil {
		return nil, err
	}

	if len(tgt.tokens) == 0 {
		return []*messaging.Message{&base}, nil
	}
	if len(tgt.tokens) > maxMulticastTokens {
		return nil, permanentf("fcm sink: message %s resolved to %d tokens; FCM accepts at most %d per send",
			msg.ID(), len(tgt.tokens), maxMulticastTokens)
	}

	out := make([]*messaging.Message, 0, len(tgt.tokens))
	for _, token := range tgt.tokens {
		copied := base
		copied.Token = token
		out = append(out, &copied)
	}
	return out, nil
}

// applyPlatforms fills in the Android, Apple and browser blocks. Each platform
// interprets priority, time to live and grouping its own way, so each gets its
// own block rather than a lowest common denominator.
func (s *Sink) applyPlatforms(m *messaging.Message, data map[string]any) error {
	android, err := s.androidConfig(data)
	if err != nil {
		return err
	}
	apns, err := s.apnsConfig(data)
	if err != nil {
		return err
	}
	webpush, err := s.webpushConfig(data)
	if err != nil {
		return err
	}
	m.Android, m.APNS, m.Webpush = android, apns, webpush
	return nil
}

func (s *Sink) fcmOptions(msg hermod.Message) *messaging.FCMOptions {
	label := msg.Metadata()[metaAnalytics]
	if label == "" {
		label = s.cfg.AnalyticsLabel
	}
	if label == "" {
		return nil
	}
	return &messaging.FCMOptions{AnalyticsLabel: label}
}

func (s *Sink) notification(msg hermod.Message, data map[string]any) (*messaging.Notification, error) {
	metadata := msg.Metadata()

	title, err := s.overridable(metadata, metaTitle, s.title, data)
	if err != nil {
		return nil, err
	}
	body, err := s.overridable(metadata, metaBody, s.body, data)
	if err != nil {
		return nil, err
	}
	image, err := s.overridable(metadata, metaImage, s.imageURL, data)
	if err != nil {
		return nil, err
	}

	if title == "" && body == "" && image == "" {
		// A message with no notification is a data-only push, which is what an
		// app that renders its own alert asks for. It is a valid message, not
		// an omission.
		return nil, nil
	}
	return &messaging.Notification{Title: title, Body: body, ImageURL: image}, nil
}

// overridable renders a templated field unless the message names a literal
// replacement in metadata.
func (s *Sink) overridable(metadata map[string]string, key string, t tmpl, data map[string]any) (string, error) {
	if v := strings.TrimSpace(metadata[key]); v != "" {
		return v, nil
	}
	return t.render(data)
}

// baseData is the data map before the operator's own additions: what the
// configured mode says a message carries.
func (s *Sink) baseData(msg hermod.Message, data map[string]any) (map[string]string, error) {
	out := map[string]string{}

	switch s.cfg.DataMode {
	case DataNone:
		return out, nil
	case DataFields:
		for _, k := range envelopeKeys {
			out[k] = fmt.Sprint(data[k])
		}
		// The row's own columns go over the envelope, not under it: a table
		// with an `id` or `table` column keeps its own value.
		for k, v := range msg.Data() {
			if slices.Contains(s.destFields, k) {
				// The column this message is addressed by. Sending it to the
				// device it addresses is at best waste and at worst a leak.
				continue
			}
			str, err := stringify(v)
			if err != nil {
				return nil, permanentf("fcm sink: field %q of message %s cannot be carried as FCM data: %v", k, msg.ID(), err)
			}
			out[k] = str
		}
		return out, nil
	default: // DataEnvelope
		formatted, err := s.format(msg)
		if err != nil {
			return nil, err
		}
		out["payload"] = string(formatted)
		for _, k := range envelopeKeys {
			out[k] = fmt.Sprint(data[k])
		}
		return out, nil
	}
}

// dataMap builds the FCM data map and enforces the size limit.
func (s *Sink) dataMap(msg hermod.Message, data map[string]any) (map[string]string, error) {
	out, err := s.baseData(msg, data)
	if err != nil {
		return nil, err
	}

	for k, t := range s.data {
		rendered, err := t.render(data)
		if err != nil {
			return nil, err
		}
		out[k] = rendered
	}

	// A key with an empty value costs bytes and tells the device nothing.
	maps.DeleteFunc(out, func(_, v string) bool { return v == "" })

	if len(out) == 0 {
		return nil, nil
	}

	limit := s.cfg.MaxDataBytes
	if limit <= 0 {
		limit = defaultMaxDataBytes
	}
	size := dataBytes(out)
	if size <= limit {
		return out, nil
	}

	switch s.cfg.OnOversize {
	case OversizeTruncate:
		return truncateData(out, limit), nil
	case OversizeDrop:
		return nil, nil
	default:
		return nil, permanentf(
			"fcm sink: the data for message %s is %d bytes and FCM accepts at most %d; set on_oversize to truncate or drop, or narrow the payload with a transformer",
			msg.ID(), size, limit)
	}
}

func (s *Sink) format(msg hermod.Message) ([]byte, error) {
	if s.formatter == nil {
		return msg.Payload(), nil
	}
	data, err := s.formatter.Format(msg)
	if err != nil {
		return nil, permanentf("fcm sink: formatting message %s failed: %v", msg.ID(), err)
	}
	return data, nil
}

func (s *Sink) androidConfig(data map[string]any) (*messaging.AndroidConfig, error) {
	a := s.cfg.Android

	collapse, err := s.androidCollapse.render(data)
	if err != nil {
		return nil, err
	}
	tag, err := s.androidTag.render(data)
	if err != nil {
		return nil, err
	}

	notification := androidNotification(a, tag)

	if a.Priority == "" && a.TTL == 0 && collapse == "" &&
		a.RestrictedPackageName == "" && notification == nil {
		return nil, nil
	}

	cfg := &messaging.AndroidConfig{
		CollapseKey:           collapse,
		Priority:              a.Priority,
		RestrictedPackageName: a.RestrictedPackageName,
		Notification:          notification,
	}
	if a.TTL > 0 {
		ttl := a.TTL
		cfg.TTL = &ttl
	}
	return cfg, nil
}

// androidNotification is nil unless something in it was configured: an empty
// block still overrides the cross-platform notification on the device.
func androidNotification(a AndroidConfig, tag string) *messaging.AndroidNotification {
	if a.ChannelID == "" && a.Sound == "" && a.Icon == "" && a.Color == "" &&
		tag == "" && a.ClickAction == "" && a.NotificationPriority == "" {
		return nil
	}
	return &messaging.AndroidNotification{
		ChannelID:   a.ChannelID,
		Sound:       a.Sound,
		Icon:        a.Icon,
		Color:       a.Color,
		Tag:         tag,
		ClickAction: a.ClickAction,
		Priority:    androidPriority(a.NotificationPriority),
	}
}

func androidPriority(name string) messaging.AndroidNotificationPriority {
	switch name {
	case "min":
		return messaging.PriorityMin
	case "low":
		return messaging.PriorityLow
	case "default":
		return messaging.PriorityDefault
	case "high":
		return messaging.PriorityHigh
	case "max":
		return messaging.PriorityMax
	}
	return 0 // unspecified; the SDK omits it
}

func (s *Sink) apnsConfig(data map[string]any) (*messaging.APNSConfig, error) {
	a := s.cfg.APNS

	collapseID, err := s.apnsCollapse.render(data)
	if err != nil {
		return nil, err
	}
	threadID, err := s.apnsThread.render(data)
	if err != nil {
		return nil, err
	}

	badge, err := s.apnsBadgeCount(data)
	if err != nil {
		return nil, err
	}

	headers := s.apnsHeaders(a, collapseID)
	aps := apsPayload(a, badge, threadID)

	if len(headers) == 0 && aps == nil {
		return nil, nil
	}
	cfg := &messaging.APNSConfig{}
	if len(headers) > 0 {
		cfg.Headers = headers
	}
	if aps != nil {
		cfg.Payload = &messaging.APNSPayload{Aps: aps}
	}
	return cfg, nil
}

// apnsBadgeCount renders the badge, which APNs takes as a number rather than a
// string. A template that renders something else is a configuration mistake
// that would otherwise surface as an opaque APNs rejection.
func (s *Sink) apnsBadgeCount(data map[string]any) (*int, error) {
	if s.apnsBadge.empty() {
		return nil, nil
	}
	rendered, err := s.apnsBadge.render(data)
	if err != nil {
		return nil, err
	}
	if rendered = strings.TrimSpace(rendered); rendered == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(rendered)
	if err != nil {
		return nil, permanentf("fcm sink: apns_badge rendered %q, which is not a whole number", rendered)
	}
	return &n, nil
}

func (s *Sink) apnsHeaders(a APNSConfig, collapseID string) map[string]string {
	headers := map[string]string{}
	if a.Priority != "" {
		headers["apns-priority"] = a.Priority
	}
	if a.Expiration > 0 {
		// APNs takes an absolute epoch second here, not a duration.
		headers["apns-expiration"] = strconv.FormatInt(s.now().Add(a.Expiration).Unix(), 10)
	}
	if collapseID != "" {
		headers["apns-collapse-id"] = collapseID
	}
	return headers
}

func apsPayload(a APNSConfig, badge *int, threadID string) *messaging.Aps {
	if a.Sound == "" && badge == nil && !a.ContentAvailable && !a.MutableContent &&
		a.Category == "" && threadID == "" {
		return nil
	}
	return &messaging.Aps{
		Sound:            a.Sound,
		Badge:            badge,
		ContentAvailable: a.ContentAvailable,
		MutableContent:   a.MutableContent,
		Category:         a.Category,
		ThreadID:         threadID,
	}
}

func (s *Sink) webpushConfig(data map[string]any) (*messaging.WebpushConfig, error) {
	w := s.cfg.Webpush

	link, err := s.webpushLink.render(data)
	if err != nil {
		return nil, err
	}

	cfg := &messaging.WebpushConfig{}
	set := false
	if link != "" {
		cfg.FCMOptions = &messaging.WebpushFCMOptions{Link: link}
		set = true
	}
	if w.Icon != "" || w.Badge != "" {
		cfg.Notification = &messaging.WebpushNotification{Icon: w.Icon, Badge: w.Badge}
		set = true
	}
	if w.TTL > 0 {
		// Web Push carries its time to live as a header, in whole seconds.
		cfg.Headers = map[string]string{"TTL": strconv.FormatInt(int64(w.TTL/time.Second), 10)}
		set = true
	}
	if !set {
		return nil, nil
	}
	return cfg, nil
}

// splitList turns a rendered field into the list of values it names. A single
// template variable holding several comma-separated tokens expands into
// several destinations, which is what makes a devices column usable directly.
func splitList(raw string) []string {
	out := make([]string, 0, 2)
	for part := range strings.SplitSeq(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// stringify renders a field value for the FCM data map, whose values are all
// strings. Composite values become JSON rather than Go's %v, which produces
// map[k:v] that nothing on a handset can parse.
func stringify(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case []byte:
		return string(t), nil
	case bool:
		return strconv.FormatBool(t), nil
	case time.Time:
		return t.Format(time.RFC3339Nano), nil
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return fmt.Sprint(t), nil
	default:
		encoded, err := json.Marshal(t)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
}

// dataBytes is the size FCM charges for a data map: every key and every value.
func dataBytes[V any](data map[string]V) int {
	total := 0
	for k, v := range data {
		total += len(k) + len(fmt.Sprint(v))
	}
	return total
}

// truncateData shortens the largest values until the map fits, marking each one
// it cuts so the receiver can tell. The envelope keys are protected: they are
// what makes the notification identifiable, and they are small enough that
// protecting them never costs a fit.
func truncateData(data map[string]string, limit int) map[string]string {
	out := maps.Clone(data)

	for dataBytes(out) > limit {
		key := longestCuttable(out)
		if key == "" {
			// Only protected keys are left. Better an oversized message that
			// FCM refuses, loudly, than one stripped of what identifies it.
			return out
		}
		over := dataBytes(out) - limit
		value := out[key]

		keep := len(value) - over - len(truncationMark)
		if keep <= 0 {
			out[key] = ""
			continue
		}
		for keep > 0 && !utf8.RuneStart(value[keep]) {
			keep--
		}
		out[key] = value[:keep] + truncationMark
	}
	return out
}

// longestCuttable is the unprotected key with the most to give back.
func longestCuttable(data map[string]string) string {
	keys := make([]string, 0, len(data))
	for k, v := range data {
		if slices.Contains(envelopeKeys, k) || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	// Sorted so a tie between two equally long values picks the same key every
	// run; an unstable choice makes the output depend on map iteration order.
	sort.Slice(keys, func(i, j int) bool {
		if len(data[keys[i]]) != len(data[keys[j]]) {
			return len(data[keys[i]]) > len(data[keys[j]])
		}
		return keys[i] < keys[j]
	})
	return keys[0]
}
