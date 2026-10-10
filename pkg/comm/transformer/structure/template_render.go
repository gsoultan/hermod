package structure

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"text/template"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("template_render", &TemplateRender{})
}

const (
	// defaultRenderBytes and maxRenderBytes bound the rendered text. A range
	// over an upstream list multiplies the template by the list's length.
	defaultRenderBytes = 64 << 10
	maxRenderBytes     = 1 << 20

	defaultRenderTarget = "rendered"
	preparedTemplateKey = "_parsed_template"

	// noValue is what text/template prints for a key a map does not have,
	// whatever its missingkey option says, because the map's element type is
	// an interface.
	noValue = "<no value>"
)

// errCallRefused replaces the template builtin "call", the one builtin that
// runs a function the data hands it.
var errCallRefused = errors.New("call is not available in templates")

// TemplateRender renders a Go text/template over the record into a field.
//
// Config:
//   - template: the template; {{.field}} reads a field. Required.
//   - targetField: where the text goes, default "rendered".
//   - strict: a field the record does not have fails the record. Off, it
//     renders as empty text.
//   - maxBytes: the longest output, default 64 KiB, at most 1 MiB. Longer
//     output fails the record.
//
// The template sees the record's data and nothing else: no functions beyond
// text/template's builtins (printf, len, index, eq and the like), no
// environment, no secrets, no files; "call" is disabled.
type TemplateRender struct{}

// preparedTemplate is a parsed template with the settings it was parsed from,
// so a config edited after Prepare is parsed again rather than ignored.
type preparedTemplate struct {
	text   string
	strict bool
	tmpl   *template.Template
}

func (r *TemplateRender) Prepare(config map[string]any) (map[string]any, error) {
	text, _ := config["template"].(string)
	strict := evaluator.ToBool(config["strict"])
	// A template that does not parse is reported by Transform, which the
	// editor's Test button runs on a config that was never prepared.
	if tmpl, err := parseTemplate(text, strict); err == nil {
		config[preparedTemplateKey] = preparedTemplate{text: text, strict: strict, tmpl: tmpl}
	}
	return config, nil
}

func (r *TemplateRender) Transform(_ context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	tmpl, strict, err := templateFor(config)
	if err != nil {
		return msg, fmt.Errorf("template_render: %w", err)
	}
	limit := min(configInt(config, "maxBytes", defaultRenderBytes), maxRenderBytes)
	w := &cappedWriter{limit: limit}
	if err := tmpl.Execute(w, msg.Data()); err != nil {
		if errors.Is(err, errOutputTooLarge) {
			return msg, fmt.Errorf("template_render: the output is longer than %d bytes", limit)
		}
		return msg, fmt.Errorf("template_render: %w", err)
	}
	out := w.String()
	if !strict {
		out = strings.ReplaceAll(out, noValue, "")
	}
	msg.SetData(configString(config, "targetField", defaultRenderTarget), out)
	return msg, nil
}

func templateFor(config map[string]any) (*template.Template, bool, error) {
	text, _ := config["template"].(string)
	strict := evaluator.ToBool(config["strict"])
	if p, ok := config[preparedTemplateKey].(preparedTemplate); ok && p.text == text && p.strict == strict {
		return p.tmpl, strict, nil
	}
	tmpl, err := parseTemplate(text, strict)
	return tmpl, strict, err
}

func parseTemplate(text string, strict bool) (*template.Template, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("write a template")
	}
	missing := "missingkey=default"
	if strict {
		missing = "missingkey=error"
	}
	tmpl, err := template.New("template_render").
		Option(missing).
		Funcs(template.FuncMap{"call": func(...any) (any, error) { return nil, errCallRefused }}).
		Parse(text)
	if err != nil {
		return nil, fmt.Errorf("the template does not parse: %w", err)
	}
	return tmpl, nil
}

var errOutputTooLarge = errors.New("output too large")

// cappedWriter collects output and fails the render once it passes limit, so
// a runaway range stops instead of filling memory.
type cappedWriter struct {
	strings.Builder
	limit int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.Len()+len(p) > w.limit {
		return 0, errOutputTooLarge
	}
	return w.Builder.Write(p)
}
