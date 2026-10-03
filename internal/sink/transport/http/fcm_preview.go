package http

import (
	"encoding/json"
	"net/http"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/pkg/comm/message"
	sinkfcm "github.com/gsoultan/hermod/pkg/comm/sink/fcm"
)

// maxFcmPreviewBytes bounds a preview request. A sink's configuration and one
// sample row are a few kilobytes; a megabyte is room for a very wide row and
// still far short of something worth sending to exhaust the server.
const maxFcmPreviewBytes = 1 << 20

// fcmPreviewResponse is the sink's own preview plus the row it was built from,
// so the editor can show which row it is describing and let it be edited.
type fcmPreviewResponse struct {
	sinkfcm.Preview
	Sample map[string]any `json:"sample"`
}

// PreviewFcmMessage answers with the FCM message a sink would send for a
// sample row, and how its data weighs against FCM's limit, without sending it.
//
// The limit is the reason this exists. FCM refuses a data map over 4096 bytes,
// the form's default sends the whole row, and the only thing that used to say a
// row was too wide was the run that dead-lettered it. The message is built by
// the sink's own code, so the size here is the size a run sends — not an
// estimate kept in step by hand in the editor.
//
// It contacts nothing: no credentials are read and none are needed.
func (h *SinkHandler) PreviewFcmMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type string `json:"type"`
		// StringMap, not map[string]string: the form's config holds a boolean
		// (`sequential`) and a plain string map refuses the whole request.
		Config hermod.StringMap `json:"config"`
		Sample map[string]any   `json:"sample"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFcmPreviewBytes)).Decode(&req); err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Type != "fcm" {
		h.JsonError(w, "this preview is only available for Firebase (FCM) sinks", http.StatusBadRequest)
		return
	}

	sample := req.Sample
	if len(sample) == 0 {
		sample = exampleRow()
	}
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	// Defaults first, then the sample through the populator every other
	// preview uses: a captured change event's own operation, table and
	// before/after images land where a run's message keeps them, not as two
	// extra columns named "after" and "before".
	msg.SetID("preview")
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable("orders")
	msg.SetSchema("public")
	message.PopulateFromMap(msg, sample)

	preview, err := factory.PreviewFCMMessage(factory.SinkConfig{Type: req.Type, Config: req.Config}, msg)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fcmPreviewResponse{Preview: preview, Sample: sample})
}
