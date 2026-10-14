// Package http is the REST surface of the dead letter page: listing the
// messages workflows could not deliver, reading one, replaying it (as it was,
// or edited, from the node that failed it or from the start), discarding it
// and exporting a selection.
package http

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
)

const (
	// maxPage bounds one page of the list.
	maxPage = 200
	// maxBulk bounds the dead letters one discard or export names.
	maxBulk = 500
	// maxBulkReplay bounds one bulk replay, which runs each replay in turn
	// inside the request.
	maxBulkReplay = 50
	// maxReplayBody bounds a replay request, an edited payload included.
	maxReplayBody = storage.DeadLetterPayloadCap + 64<<10
	// replayTimeout bounds one replay's walk.
	replayTimeout = 2 * time.Minute
)

// Handler serves the dead letter routes.
type Handler struct {
	*handlers.Handler
	// replaying holds the ids of dead letters being replayed in this process,
	// so a double click cannot deliver the message twice.
	replaying sync.Map
}

// NewHandler wraps the shared API handler.
func NewHandler(h *handlers.Handler) *Handler {
	return &Handler{Handler: h}
}

// RegisterRoutes mounts the routes. Reads need any role on the dead letter's
// vhost; replaying and discarding need Editor.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/dead-letters", h.List)
	mux.HandleFunc("GET /api/dead-letters/stats", h.Stats)
	mux.HandleFunc("GET /api/dead-letters/export", h.Export)
	mux.HandleFunc("GET /api/dead-letters/{id}", h.Get)
	mux.Handle("POST /api/dead-letters/{id}/replay", h.EditorOnly(h.Replay))
	mux.Handle("POST /api/dead-letters/replay", h.EditorOnly(h.ReplayMany))
	mux.Handle("POST /api/dead-letters/discard", h.EditorOnly(h.Discard))
}

func (h *Handler) store(w http.ResponseWriter) (storage.DeadLetterStore, bool) {
	ds, ok := h.Storage.(storage.DeadLetterStore)
	if !ok {
		h.JsonError(w, storage.ErrDeadLettersUnsupported.Error(), http.StatusNotImplemented)
	}
	return ds, ok
}

// scope is the vhosts the caller may see, or nil for all of them, narrowed to
// the vhost the request names. It writes a 403 for a vhost the caller may not
// see.
func (h *Handler) scope(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	role, vhosts := h.GetRoleAndVHosts(r)
	all := role == storage.RoleAdministrator || slices.Contains(vhosts, "*")
	if v := r.URL.Query().Get("vhost"); v != "" && v != "all" {
		if !all && !h.HasVHostAccess(v, vhosts) {
			h.JsonError(w, "Forbidden: no access to vhost "+v, http.StatusForbidden)
			return nil, false
		}
		return []string{v}, true
	}
	if all {
		return nil, true
	}
	return append([]string{}, vhosts...), true
}

func (h *Handler) mayAccess(r *http.Request, d storage.DeadLetter) bool {
	role, vhosts := h.GetRoleAndVHosts(r)
	return role == storage.RoleAdministrator || h.HasVHostAccess(d.VHost, vhosts)
}

func username(r *http.Request) string {
	if u, ok := r.Context().Value(handlers.UserContextKey).(*storage.User); ok && u != nil {
		return u.Username
	}
	return ""
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// row is a dead letter with the name of its workflow, which the page shows.
type row struct {
	storage.DeadLetter
	WorkflowName string `json:"workflow_name,omitempty"`
}

// named looks up each workflow once.
func (h *Handler) named(ctx context.Context, ds []storage.DeadLetter) []row {
	names := map[string]string{}
	out := make([]row, len(ds))
	for i, d := range ds {
		name, seen := names[d.WorkflowID]
		if !seen {
			if wf, err := h.Storage.GetWorkflow(ctx, d.WorkflowID); err == nil {
				name = wf.Name
			}
			names[d.WorkflowID] = name
		}
		out[i] = row{DeadLetter: d, WorkflowName: name}
	}
	return out
}

func parseFilter(r *http.Request) (storage.DeadLetterFilter, error) {
	q := r.URL.Query()
	f := storage.DeadLetterFilter{
		WorkflowID: q.Get("workflow_id"),
		Where:      q.Get("where"),
		Status:     storage.DeadLetterStatus(q.Get("status")),
		Search:     strings.TrimSpace(q.Get("search")),
		Limit:      50,
	}
	switch f.Status {
	case "", storage.DeadLetterQueued, storage.DeadLetterReplayed, storage.DeadLetterDiscarded:
	default:
		return f, fmt.Errorf("unknown status %q", f.Status)
	}
	for name, into := range map[string]*time.Time{"since": &f.Since, "until": &f.Until} {
		if v := q.Get(name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return f, fmt.Errorf("%s must be an RFC 3339 time", name)
			}
			*into = t
		}
	}
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 {
		f.Limit = min(v, maxPage)
	}
	if v, err := strconv.Atoi(q.Get("offset")); err == nil && v > 0 {
		f.Offset = v
	}
	return f, nil
}

// List returns a page of dead letters, newest first, and the total.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ds, ok := h.store(w)
	if !ok {
		return
	}
	f, err := parseFilter(r)
	if err != nil {
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if f.VHosts, ok = h.scope(w, r); !ok {
		return
	}
	page, total, err := ds.ListDeadLetters(r.Context(), f)
	if err != nil {
		h.JsonError(w, "Failed to list dead letters", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"data": h.named(r.Context(), page), "total": total})
}

// Stats is the summary the page leads with.
func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	ds, ok := h.store(w)
	if !ok {
		return
	}
	vhosts, ok := h.scope(w, r)
	if !ok {
		return
	}
	st, err := ds.DeadLetterStats(r.Context(), vhosts, time.Now())
	if err != nil {
		h.JsonError(w, "Failed to summarise dead letters", http.StatusInternalServerError)
		return
	}
	writeJSON(w, st)
}

// deadLetter reads the one named in the path, if the caller may see it,
// having written the refusal when not. Another vhost's answers "not found",
// so its existence is not disclosed either.
func (h *Handler) deadLetter(w http.ResponseWriter, r *http.Request, ds storage.DeadLetterStore, id string) (storage.DeadLetter, bool) {
	d, err := ds.GetDeadLetter(r.Context(), id)
	if err == nil && !h.mayAccess(r, d) {
		err = storage.ErrNotFound
	}
	switch {
	case errors.Is(err, storage.ErrNotFound):
		h.JsonError(w, "Dead letter not found", http.StatusNotFound)
		return d, false
	case err != nil:
		h.JsonError(w, "Failed to read the dead letter", http.StatusInternalServerError)
		return d, false
	}
	return d, true
}

// Get returns one dead letter: the message, its metadata and its history.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ds, ok := h.store(w)
	if !ok {
		return
	}
	d, ok := h.deadLetter(w, r, ds, r.PathValue("id"))
	if !ok {
		return
	}
	writeJSON(w, h.named(r.Context(), []storage.DeadLetter{d})[0])
}

// replayRequest is a replay. Payload, when set, is the edited message as JSON
// text; Test asks what each node would make of it, and writes nothing.
type replayRequest struct {
	Payload   *string `json:"payload"`
	FromStart bool    `json:"from_start"`
	Test      bool    `json:"test"`
}

func (q replayRequest) options() registry.DeadLetterReplay {
	opt := registry.DeadLetterReplay{FromStart: q.FromStart}
	if q.Payload != nil {
		opt.Payload = []byte(*q.Payload)
	}
	return opt
}

// replayOutcome is what one replay came to.
type replayOutcome struct {
	ID string `json:"id"`
	// Status is replayed, failed (the message failed again and stays in the
	// queue), or why it was not replayed.
	Status string              `json:"status"`
	Error  string              `json:"error,omitempty"`
	Result *registry.RunResult `json:"result,omitempty"`
}

// Replay sends one dead letter again, or with test set shows what each node
// would make of it.
func (h *Handler) Replay(w http.ResponseWriter, r *http.Request) {
	ds, ok := h.store(w)
	if !ok {
		return
	}
	var req replayRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReplayBody)).Decode(&req); err != nil {
		h.JsonError(w, "the request body must be a JSON replay request", http.StatusBadRequest)
		return
	}
	if req.Payload != nil && !json.Valid([]byte(*req.Payload)) {
		h.JsonError(w, "the edited payload is not valid JSON", http.StatusBadRequest)
		return
	}
	d, ok := h.deadLetter(w, r, ds, r.PathValue("id"))
	if !ok {
		return
	}
	if !h.replayable(w, d, req) {
		return
	}
	wf, err := h.Storage.GetWorkflow(r.Context(), d.WorkflowID)
	if err != nil {
		h.JsonError(w, "The workflow this message belongs to no longer exists", http.StatusConflict)
		return
	}

	if req.Test {
		steps, err := h.Registry.PreviewDeadLetterReplay(r.Context(), wf, d, req.options())
		if err != nil {
			h.JsonError(w, err.Error(), replayErrorStatus(err))
			return
		}
		writeJSON(w, map[string]any{"steps": steps})
		return
	}

	out := h.replay(r, ds, wf, d, req)
	if out.Status != "replayed" && out.Status != "failed" && out.Result == nil {
		h.JsonError(w, out.Error, http.StatusConflict)
		return
	}
	writeJSON(w, out)
}

// replayable answers the request itself, and says false, when d cannot be
// replayed as asked.
func (h *Handler) replayable(w http.ResponseWriter, d storage.DeadLetter, req replayRequest) bool {
	switch {
	case d.Status != storage.DeadLetterQueued:
		h.JsonError(w, "This dead letter was already "+string(d.Status), http.StatusConflict)
	case d.Truncated && req.Payload == nil:
		h.JsonError(w, registry.ErrDeadLetterTruncated.Error(), http.StatusUnprocessableEntity)
	case h.Registry == nil:
		h.JsonError(w, "Replaying needs a running control plane", http.StatusServiceUnavailable)
	default:
		return true
	}
	return false
}

func replayErrorStatus(err error) int {
	if errors.Is(err, registry.ErrDeadLetterTruncated) || errors.Is(err, registry.ErrDeadLetterNodeGone) {
		return http.StatusUnprocessableEntity
	}
	return http.StatusBadRequest
}

// replay runs one replay and settles the dead letter when it was delivered.
// A replay that fails again leaves it in the queue, with the attempt in its
// history.
func (h *Handler) replay(r *http.Request, ds storage.DeadLetterStore, wf storage.Workflow, d storage.DeadLetter, req replayRequest) replayOutcome {
	out := replayOutcome{ID: d.ID}
	if _, busy := h.replaying.LoadOrStore(d.ID, struct{}{}); busy {
		out.Status, out.Error = "busy", "this dead letter is already being replayed"
		return out
	}
	defer h.replaying.Delete(d.ID)

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), replayTimeout)
	defer cancel()
	res, err := h.Registry.ReplayDeadLetter(ctx, wf, d, req.options())
	if err != nil {
		out.Status, out.Error = "not_replayed", err.Error()
		return out
	}
	out.Result = &res
	by := username(r)
	detail := describeReplay(req)
	if res.Status == registry.RunFailed {
		out.Status, out.Error = "failed", firstStepError(res)
		_ = ds.AddDeadLetterEvent(ctx, d.ID, storage.NewDeadLetterEvent("replay_failed", by, detail+": "+out.Error))
		return out
	}
	if err := ds.SettleDeadLetter(ctx, d.ID, storage.DeadLetterReplayed, storage.NewDeadLetterEvent("replayed", by, detail)); err != nil &&
		!errors.Is(err, storage.ErrDeadLetterSettled) {
		h.Registry.GetLogger().Error("A dead letter was replayed but could not be marked replayed",
			"dead_letter_id", d.ID, "error", err)
	}
	out.Status = "replayed"
	h.RecordAuditLog(r, "INFO", "Replayed dead letter "+d.ID, "REPLAY_DEAD_LETTER", d.WorkflowID, "", d.SinkID,
		map[string]string{"dead_letter_id": d.ID, "message_id": d.MessageID, "detail": detail})
	return out
}

func describeReplay(req replayRequest) string {
	parts := []string{}
	if req.Payload != nil {
		parts = append(parts, "edited")
	}
	if req.FromStart {
		parts = append(parts, "from the start")
	} else {
		parts = append(parts, "from the failed node")
	}
	return strings.Join(parts, ", ")
}

func firstStepError(res registry.RunResult) string {
	for _, s := range res.Steps {
		if s.Error != "" {
			return s.Error
		}
	}
	return "the message failed again"
}

// bulkRequest names dead letters for a replay, discard or export.
type bulkRequest struct {
	IDs       []string `json:"ids"`
	FromStart bool     `json:"from_start"`
}

func (h *Handler) readBulk(w http.ResponseWriter, r *http.Request) (bulkRequest, bool) {
	var req bulkRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		h.JsonError(w, `the request body must be {"ids": [...]}`, http.StatusBadRequest)
		return req, false
	}
	req.IDs = dedupe(req.IDs)
	if len(req.IDs) == 0 || len(req.IDs) > maxBulk {
		h.JsonError(w, fmt.Sprintf("name between 1 and %d dead letters", maxBulk), http.StatusBadRequest)
		return req, false
	}
	return req, true
}

func dedupe(ids []string) []string {
	seen := map[string]bool{}
	out := ids[:0:0]
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// ReplayMany replays each named dead letter as it was, one after another.
func (h *Handler) ReplayMany(w http.ResponseWriter, r *http.Request) {
	ds, ok := h.store(w)
	if !ok {
		return
	}
	req, ok := h.readBulk(w, r)
	if !ok {
		return
	}
	if len(req.IDs) > maxBulkReplay {
		h.JsonError(w, fmt.Sprintf("replay at most %d dead letters at once", maxBulkReplay), http.StatusBadRequest)
		return
	}
	if h.Registry == nil {
		h.JsonError(w, "Replaying needs a running control plane", http.StatusServiceUnavailable)
		return
	}
	results := make([]replayOutcome, 0, len(req.IDs))
	workflows := map[string]*storage.Workflow{}
	for _, id := range req.IDs {
		d, status := h.bulkTarget(r, ds, id)
		if status != "" {
			results = append(results, replayOutcome{ID: id, Status: status})
			continue
		}
		if d.Truncated {
			results = append(results, replayOutcome{ID: id, Status: "not_replayed", Error: registry.ErrDeadLetterTruncated.Error()})
			continue
		}
		wf, seen := workflows[d.WorkflowID]
		if !seen {
			if got, err := h.Storage.GetWorkflow(r.Context(), d.WorkflowID); err == nil {
				wf = &got
			}
			workflows[d.WorkflowID] = wf
		}
		if wf == nil {
			results = append(results, replayOutcome{ID: id, Status: "not_replayed", Error: "the workflow no longer exists"})
			continue
		}
		out := h.replay(r, ds, *wf, d, replayRequest{FromStart: req.FromStart})
		out.Result = nil
		results = append(results, out)
	}
	writeJSON(w, map[string]any{"results": results})
}

// bulkTarget reads one named dead letter for a bulk action, or says why it is
// skipped: not_found (including another vhost's) or already_settled.
func (h *Handler) bulkTarget(r *http.Request, ds storage.DeadLetterStore, id string) (storage.DeadLetter, string) {
	d, err := ds.GetDeadLetter(r.Context(), id)
	if err != nil || !h.mayAccess(r, d) {
		return d, "not_found"
	}
	if d.Status != storage.DeadLetterQueued {
		return d, "already_settled"
	}
	return d, ""
}

// Discard takes each named dead letter out of the queue. The message stays in
// the workflow's dead-letter sink; this is the page's record that it was
// dealt with.
func (h *Handler) Discard(w http.ResponseWriter, r *http.Request) {
	ds, ok := h.store(w)
	if !ok {
		return
	}
	req, ok := h.readBulk(w, r)
	if !ok {
		return
	}
	by := username(r)
	results := make([]replayOutcome, 0, len(req.IDs))
	for _, id := range req.IDs {
		d, status := h.bulkTarget(r, ds, id)
		if status == "" {
			err := ds.SettleDeadLetter(r.Context(), id, storage.DeadLetterDiscarded, storage.NewDeadLetterEvent("discarded", by, ""))
			switch {
			case err == nil:
				status = "discarded"
				h.RecordAuditLog(r, "INFO", "Discarded dead letter "+id, "DISCARD_DEAD_LETTER", d.WorkflowID, "", d.SinkID,
					map[string]string{"dead_letter_id": id, "message_id": d.MessageID})
			case errors.Is(err, storage.ErrDeadLetterSettled):
				status = "already_settled"
			default:
				status = "failed"
			}
		}
		results = append(results, replayOutcome{ID: id, Status: status})
	}
	writeJSON(w, map[string]any{"results": results})
}

// Export writes the named dead letters (ids, comma separated), or else those
// the filter matches, as CSV with their payloads. Dead letters the caller may
// not see are left out.
func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	ds, ok := h.store(w)
	if !ok {
		return
	}
	var ids []string
	if v := r.URL.Query().Get("ids"); v != "" {
		ids = dedupe(strings.Split(v, ","))
	} else {
		f, err := parseFilter(r)
		if err != nil {
			h.JsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		if f.VHosts, ok = h.scope(w, r); !ok {
			return
		}
		f.Limit, f.Offset = maxBulk, 0
		page, _, err := ds.ListDeadLetters(r.Context(), f)
		if err != nil {
			h.JsonError(w, "Failed to list dead letters", http.StatusInternalServerError)
			return
		}
		for _, d := range page {
			ids = append(ids, d.ID)
		}
	}
	if len(ids) > maxBulk {
		h.JsonError(w, fmt.Sprintf("export at most %d dead letters at once", maxBulk), http.StatusBadRequest)
		return
	}
	var letters []storage.DeadLetter
	for _, id := range ids {
		if d, err := ds.GetDeadLetter(r.Context(), id); err == nil && h.mayAccess(r, d) {
			letters = append(letters, d)
		}
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="dead-letters-`+time.Now().UTC().Format("20060102-150405")+`.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "workflow_id", "workflow", "message_id", "node_id", "sink_id", "failed_at", "status", "error", "payload", "truncated"})
	for _, d := range h.named(r.Context(), letters) {
		_ = cw.Write(defuse([]string{
			d.ID, d.WorkflowID, d.WorkflowName, d.MessageID, d.NodeID, d.SinkID, d.FailedAt.UTC().Format(time.RFC3339),
			string(d.Status), d.Error, string(d.Payload), strconv.FormatBool(d.Truncated),
		}))
	}
	cw.Flush()
}

// defuse stops a spreadsheet reading a cell as a formula. Error messages and
// payloads are text other systems wrote.
func defuse(cells []string) []string {
	for i, c := range cells {
		if c != "" && strings.ContainsRune("=+-@\t\r", rune(c[0])) {
			cells[i] = "'" + c
		}
	}
	return cells
}
