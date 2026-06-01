package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod/internal/storage"
)

type FormField struct {
	ID string `json:"id"`
	// Input fields
	Name            string   `json:"name"`
	Label           string   `json:"label"`
	Type            string   `json:"type"`
	Required        bool     `json:"required"`
	Options         []string `json:"options"`
	Placeholder     string   `json:"placeholder"`
	Help            string   `json:"help"`
	NumberKind      string   `json:"number_kind"`
	Render          string   `json:"render"`
	VerifyEmail     bool     `json:"verify_email"`
	RejectIfInvalid bool     `json:"reject_if_invalid"`
	Min             float64  `json:"min"`
	Max             float64  `json:"max"`
	Step            float64  `json:"step"`
	StartLabel      string   `json:"start_label"`
	EndLabel        string   `json:"end_label"`
	// Layout metadata
	Section string `json:"section"`
	Width   string `json:"width"` // auto | half | full
	// Layout-only content
	Content string `json:"content"` // for heading/text_block
	Level   int    `json:"level"`   // 1..3 for heading
}

// HandleForm receives form submissions (JSON, x-www-form-urlencoded, or multipart)
func (h *Handler) HandleForm(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if path == "" {
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}
	fullPath := "/api/forms/" + path

	ct := r.Header.Get("Content-Type")
	payload := make(map[string]any)

	if strings.Contains(ct, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
	} else {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "Failed to parse form", http.StatusBadRequest)
				return
			}
		}
		for k, v := range r.PostForm {
			if len(v) > 0 {
				payload[k] = v[0]
			}
		}
	}

	sendErr := func(msg string, code int) {
		if strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Error(w, msg, code)
		} else {
			h.JsonError(w, msg, code)
		}
	}

	// Verify API Key or Referrer if configured
	var sourceID string
	var srcCfg map[string]string
	{
		sources, _, e := h.Storage.ListSources(r.Context(), storage.CommonFilter{})
		if e == nil {
			for _, src := range sources {
				if src.Type == "form" && src.Config["path"] == fullPath {
					sourceID = src.ID
					srcCfg = src.Config
					break
				}
			}
		}
	}

	if sourceID == "" {
		sendErr("Form not found", http.StatusNotFound)
		return
	}

	// Rate limiting
	limit := 60 // default 60 per hour
	if l, err := strconv.Atoi(srcCfg["rate_limit"]); err == nil && l > 0 {
		limit = l
	}
	if h.IsRateLimited(r, sourceID, limit) {
		sendErr("Too many requests", http.StatusTooManyRequests)
		return
	}

	// Bot protection
	enableBot := true
	if srcCfg["bot_protection"] == "false" {
		enableBot = false
	}
	minMs := 2000 // default 2 seconds
	if m, err := strconv.Atoi(srcCfg["min_submit_time"]); err == nil {
		minMs = m
	}
	if err := h.BotProtectionCheck(r, payload, enableBot, minMs, srcCfg); err != nil {
		sendErr(err.Error(), http.StatusForbidden)
		return
	}

	// Validate required fields
	var fields []FormField
	if fStr := srcCfg["fields"]; fStr != "" {
		_ = json.Unmarshal([]byte(fStr), &fields)
	}

	for _, f := range fields {
		if f.Required {
			val := payload[f.Name]
			if val == nil || fmt.Sprint(val) == "" {
				sendErr(fmt.Sprintf("Field %s is required", f.Label), http.StatusBadRequest)
				return
			}
		}
	}

	msgID := uuid.New().String()
	body, _ := json.Marshal(payload)

	// Persist submission
	submission := storage.FormSubmission{
		ID:        msgID,
		Timestamp: time.Now(),
		Path:      fullPath,
		Data:      body,
		Status:    "pending",
	}
	if err := h.Storage.CreateFormSubmission(r.Context(), submission); err != nil {
		sendErr("Failed to persist submission: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Record audit log
	h.RecordAuditLog(r, "INFO", "Form submitted: "+path, "form_submit", "", sourceID, "", payload)

	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		redirect := srcCfg["redirect_url"]
		if redirect == "" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<h1>Thank you!</h1><p>Your submission has been received.</p>"))
			return
		}
		http.Redirect(w, r, redirect, http.StatusFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "dispatched", "id": msgID})
}

// ServeFormPage renders a public HTML form for a configured form source path.
func (h *Handler) ServeFormPage(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if path == "" {
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}
	fullPath := "/api/forms/" + path

	// Find the matching source
	sources, _, err := h.Storage.ListSources(r.Context(), storage.CommonFilter{})
	if err != nil {
		http.Error(w, "Failed to load sources", http.StatusInternalServerError)
		return
	}
	var cfg map[string]string
	for _, src := range sources {
		if src.Type == "form" && src.Config["path"] == fullPath {
			cfg = src.Config
			break
		}
	}
	if cfg == nil {
		http.NotFound(w, r)
		return
	}

	title := cfg["title"]
	if title == "" {
		title = "Submit Form"
	}
	description := cfg["description"]

	var fields []FormField
	if fStr := cfg["fields"]; fStr != "" {
		_ = json.Unmarshal([]byte(fStr), &fields)
	}

	// Generate CSRF-like token for bot protection
	token := uuid.New().String()
	issued := time.Now().UnixMilli()

	// Set cookies for validation
	http.SetCookie(w, &http.Cookie{
		Name:     "hf_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "hf_issued",
		Value:    fmt.Sprint(issued),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html><head>")
	sb.WriteString("<title>" + h.HtmlEscape(title) + "</title>")
	sb.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")
	sb.WriteString("<style>")
	sb.WriteString("body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; line-height: 1.5; color: #333; max-width: 600px; margin: 40px auto; padding: 0 20px; background: #f9f9f9; }")
	sb.WriteString(".card { background: white; padding: 30px; border-radius: 8px; box-shadow: 0 2px 10px rgba(0,0,0,0.05); }")
	sb.WriteString("h1 { margin-top: 0; font-size: 24px; }")
	sb.WriteString(".desc { color: #666; margin-bottom: 25px; }")
	sb.WriteString(".field { margin-bottom: 20px; }")
	sb.WriteString("label { display: block; margin-bottom: 5px; font-weight: 500; font-size: 14px; }")
	sb.WriteString("input[type=text], input[type=email], input[type=number], input[type=date], textarea, select { width: 100%; padding: 10px; border: 1px solid #ddd; border-radius: 4px; box-sizing: border-box; font-size: 16px; }")
	sb.WriteString("textarea { height: 100px; resize: vertical; }")
	sb.WriteString("button { background: #007bff; color: white; border: none; padding: 12px 20px; border-radius: 4px; cursor: pointer; font-size: 16px; font-weight: 600; width: 100%; }")
	sb.WriteString("button:hover { background: #0056b3; }")
	sb.WriteString(".help { font-size: 12px; color: #888; margin-top: 4px; }")
	sb.WriteString(".row { display: flex; gap: 15px; } .row > div { flex: 1; }")
	sb.WriteString("</style></head><body><div class=\"card\">")
	sb.WriteString("<h1>" + h.HtmlEscape(title) + "</h1>")
	if description != "" {
		sb.WriteString("<p class=\"desc\">" + h.HtmlEscape(description) + "</p>")
	}
	sb.WriteString("<form method=\"POST\" action=\"" + fullPath + "\" enctype=\"multipart/form-data\">")
	sb.WriteString("<input type=\"hidden\" name=\"hf_token\" value=\"" + token + "\">")
	sb.WriteString("<input type=\"hidden\" name=\"website\" value=\"\">") // Honeypot

	for _, f := range fields {
		sb.WriteString(h.RenderField(f))
	}

	submitLabel := cfg["submit_label"]
	if submitLabel == "" {
		submitLabel = "Submit"
	}
	sb.WriteString("<div style=\"margin-top:30px;\"><button type=\"submit\">" + h.HtmlEscape(submitLabel) + "</button></div>")
	sb.WriteString("</form></div></body></html>")

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(sb.String()))
}

// ServeFormScript returns a small JS snippet to embed a form.
func (h *Handler) ServeFormScript(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if path == "" {
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}

	script := `(function() {
    function init() {
        const containers = document.querySelectorAll('[data-hermod-form="` + path + `"]');
        containers.forEach(container => {
            const iframe = document.createElement('iframe');
            iframe.src = window.location.origin + '/forms/` + path + `';
            iframe.style.width = '100%';
            iframe.style.border = 'none';
            iframe.style.overflow = 'hidden';
            iframe.style.minHeight = '500px';
            container.appendChild(iframe);

            window.addEventListener('message', function(e) {
                if (e.data.type === 'hermod-resize') {
                    iframe.style.height = e.data.height + 'px';
                }
            });
        });
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();`

	w.Header().Set("Content-Type", "application/javascript")
	_, _ = w.Write([]byte(script))
}

func (h *Handler) RenderField(f FormField) string {
	if f.Name == "" {
		return ""
	}
	label := f.Label
	if label == "" {
		label = strings.Title(strings.ReplaceAll(f.Name, "_", " "))
	}
	star := ""
	requiredAttr := ""
	if f.Required {
		star = " *"
		requiredAttr = " required"
	}
	placeholderAttr := ""
	if f.Placeholder != "" {
		placeholderAttr = " placeholder=\"" + h.HtmlEscape(f.Placeholder) + "\""
	}
	helpHTML := ""
	if f.Help != "" {
		helpHTML = "<div class=\"help\">" + h.HtmlEscape(f.Help) + "</div>"
	}
	colClass := "col-12"
	if strings.EqualFold(f.Width, "half") {
		colClass = "col-6"
	}
	var sb strings.Builder
	sb.WriteString("<div class=\"field " + colClass + "\">")
	sb.WriteString("<label for=\"" + h.HtmlEscape(f.Name) + "\">" + h.HtmlEscape(label) + star + "</label>")
	switch strings.ToLower(f.Type) {
	case "textarea":
		sb.WriteString("<textarea id=\"" + h.HtmlEscape(f.Name) + "\" name=\"" + h.HtmlEscape(f.Name) + "\"" + placeholderAttr + requiredAttr + "></textarea>")
	case "text":
		sb.WriteString("<input type=\"text\" id=\"" + h.HtmlEscape(f.Name) + "\" name=\"" + h.HtmlEscape(f.Name) + "\"" + placeholderAttr + requiredAttr + "/>")
	case "number":
		stepAttr := " step=\"any\""
		if strings.ToLower(f.NumberKind) == "integer" {
			stepAttr = " step=\"1\""
		}
		sb.WriteString("<input type=\"number\" id=\"" + h.HtmlEscape(f.Name) + "\" name=\"" + h.HtmlEscape(f.Name) + "\"" + stepAttr + placeholderAttr + requiredAttr + "/>")
	case "email":
		sb.WriteString("<input type=\"email\" id=\"" + h.HtmlEscape(f.Name) + "\" name=\"" + h.HtmlEscape(f.Name) + "\"" + placeholderAttr + requiredAttr + "/>")
	case "date":
		sb.WriteString("<input type=\"date\" id=\"" + h.HtmlEscape(f.Name) + "\" name=\"" + h.HtmlEscape(f.Name) + "\"" + placeholderAttr + requiredAttr + "/>")
	case "datetime":
		sb.WriteString("<input type=\"datetime-local\" id=\"" + h.HtmlEscape(f.Name) + "\" name=\"" + h.HtmlEscape(f.Name) + "\"" + placeholderAttr + requiredAttr + "/>")
	case "date_range":
		left := "Start"
		right := "End"
		if f.StartLabel != "" {
			left = f.StartLabel
		}
		if f.EndLabel != "" {
			right = f.EndLabel
		}
		sb.WriteString("<div class=\"row\"><div><label>" + h.HtmlEscape(left) + star + "</label><input type=\"date\" name=\"" + h.HtmlEscape(f.Name) + "_start\"" + requiredAttr + "/></div><div><label>" + h.HtmlEscape(right) + star + "</label><input type=\"date\" name=\"" + h.HtmlEscape(f.Name) + "_end\"" + requiredAttr + "/></div></div>")
	case "image":
		sb.WriteString("<input type=\"file\" accept=\"image/*\" id=\"" + h.HtmlEscape(f.Name) + "\" name=\"" + h.HtmlEscape(f.Name) + "\"" + requiredAttr + "/>")
	case "multiple":
		sb.WriteString("<select multiple id=\"" + h.HtmlEscape(f.Name) + "\" name=\"" + h.HtmlEscape(f.Name) + "\"" + requiredAttr + ">")
		for _, opt := range f.Options {
			sb.WriteString("<option value=\"" + h.HtmlEscape(opt) + "\">" + h.HtmlEscape(opt) + "</option>")
		}
		sb.WriteString("</select>")
	case "one":
		if f.Render == "radio" {
			for _, opt := range f.Options {
				sb.WriteString("<label style=\"display:flex;gap:8px;align-items:center;margin:6px 0;\"><input type=\"radio\" name=\"" + h.HtmlEscape(f.Name) + "\" value=\"" + h.HtmlEscape(opt) + "\"" + requiredAttr + ">" + h.HtmlEscape(opt) + "</label>")
			}
		} else {
			sb.WriteString("<select id=\"" + h.HtmlEscape(f.Name) + "\" name=\"" + h.HtmlEscape(f.Name) + "\"" + requiredAttr + ">")
			sb.WriteString("<option value=\"\" disabled selected>Select…</option>")
			for _, opt := range f.Options {
				sb.WriteString("<option value=\"" + h.HtmlEscape(opt) + "\">" + h.HtmlEscape(opt) + "</option>")
			}
			sb.WriteString("</select>")
		}
	case "scale":
		minAttr := ""
		if f.Min != 0 {
			minAttr = fmt.Sprintf(" min=\"%v\"", f.Min)
		}
		maxAttr := ""
		if f.Max != 0 {
			maxAttr = fmt.Sprintf(" max=\"%v\"", f.Max)
		}
		stepAttr := ""
		if f.Step != 0 {
			stepAttr = fmt.Sprintf(" step=\"%v\"", f.Step)
		}
		sb.WriteString("<input type=\"range\" id=\"" + h.HtmlEscape(f.Name) + "\" name=\"" + h.HtmlEscape(f.Name) + "\"" + minAttr + maxAttr + stepAttr + requiredAttr + "/>")
	default:
		sb.WriteString("<input type=\"text\" id=\"" + h.HtmlEscape(f.Name) + "\" name=\"" + h.HtmlEscape(f.Name) + "\"" + placeholderAttr + requiredAttr + "/>")
	}
	sb.WriteString(helpHTML)
	sb.WriteString("</div>")
	return sb.String()
}
