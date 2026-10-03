package fcm

import (
	"fmt"
	"strings"

	"firebase.google.com/go/v4/messaging"
	"github.com/gsoultan/hermod"
)

// previewLargest is how many data keys a preview names. Enough to see what is
// taking the room without listing a wide row back at its owner.
const previewLargest = 3

// DataKeySize is one key of the data map and what it costs against the limit.
type DataKeySize struct {
	Key   string `json:"key"`
	Bytes int    `json:"bytes"`
}

// Preview is what one hermod message would become, without sending it.
type Preview struct {
	// Message is the FCM message as it would go on the wire — the first of
	// them, when a token field fans out to several devices. Nil when the sink
	// would refuse the message; Refused then says why.
	Message *messaging.Message `json:"message,omitempty"`
	// Recipients is how many devices the message is addressed to. Zero for a
	// topic or a condition, which name an audience rather than devices.
	Recipients int `json:"recipients"`

	// DataBytes is what the data map weighs before the size policy acts on it,
	// and SentDataBytes what it weighs after. They differ when the policy
	// truncated or dropped the data.
	DataBytes     int `json:"data_bytes"`
	SentDataBytes int `json:"sent_data_bytes"`
	// Limit is the size the data map is held to.
	Limit int `json:"limit"`
	// Largest are the keys costing the most, largest first.
	Largest []DataKeySize `json:"largest,omitempty"`

	// Refused is the failure a run would report for this message. Empty means
	// it would be sent.
	Refused string `json:"refused,omitempty"`
}

// PreviewMessage builds the FCM message msg would become under cfg, through the
// same code Write sends with, and reports its size against the data limit.
//
// It contacts nothing and needs no credentials. The error is for a
// configuration that could build no message at all — a template that does not
// parse, two destinations. A message this particular row cannot produce is not
// an error: it comes back as Preview.Refused, because that is the answer the
// caller asked for.
func PreviewMessage(cfg Config, msg hermod.Message) (Preview, error) {
	if cfg.Action == ActionSubscribe || cfg.Action == ActionUnsubscribe {
		return Preview{}, fmt.Errorf("fcm sink: the %s action moves devices between topics and sends no message to preview", cfg.Action)
	}

	s, err := newUnconnected(cfg)
	if err != nil {
		return Preview{}, err
	}

	out := Preview{Limit: s.dataLimit()}

	// Measured apart from build, so a row the size policy refuses still
	// reports how big it was and what made it so.
	if data, err := s.assembleData(msg, renderData(msg)); err == nil {
		out.DataBytes = dataBytes(data)
		out.Largest = largestData(data, previewLargest)
	}

	built, err := s.build(msg)
	if err != nil {
		// The classification is for the retry machinery. To a reader it is a
		// prefix in front of the sentence they wanted.
		out.Refused = strings.TrimPrefix(err.Error(), ErrPermanent.Error()+": ")
		return out, nil
	}

	out.Message = built[0]
	out.SentDataBytes = dataBytes(built[0].Data)
	if built[0].Token != "" {
		out.Recipients = len(built)
	}
	return out, nil
}
