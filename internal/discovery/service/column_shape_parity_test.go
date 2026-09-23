package service

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// The JSON-text column was found by accident -- someone happened to try a
// string. This enumerates the shapes instead of guessing at them, and pins both
// halves of the answer: which shapes the editor and the engine bind identically,
// and which ones cannot agree and why.
//
// It ratchets in both directions on purpose. A shape that agrees today and stops
// agreeing is a regression. A shape documented as unable to agree that starts
// agreeing means the limitation was lifted, and the table should say so rather
// than keep claiming otherwise.
//
// The rule the table makes visible: an envelope path (`after.col`) always
// agrees, because both sides reach it through JSON. A bare path (`col`) agrees
// only for types JSON can carry losslessly -- the engine keeps the driver's Go
// type while the editor's sample has been through JSON and cannot know what it
// was. That is the whole reason a key should be spelled `{{.col}}` and never
// `{{.after.col}}`: the bare form is the one that keeps an int64 an int64.

type columnShape struct {
	name string
	val  any
	// bareDiffers records that `{{.col}}` cannot agree because the sample lost
	// the Go type on its way through JSON, with the reason.
	bareDiffers string
	// deepDiffers records the same for `{{.col.k}}` / `{{.after.col.k}}`.
	deepDiffers string
	// engineDeep is what the engine must bind for `{{.col.k}}`, when the shape
	// carries a "k" at all. Agreement alone is not the guarantee: two sides that
	// both resolve to nothing agree perfectly and enrich nothing, which is the
	// bug this whole file exists because of.
	engineDeep any
}

func columnShapes() []columnShape {
	const obj = `{"k":"v","n":5}`
	return []columnShape{
		{name: "decoded object", val: map[string]any{"k": "v", "n": 5}, engineDeep: "v",
			bareDiffers: "the nested int 5 comes back from the sample as float64"},
		{name: "JSON text", val: obj, engineDeep: "v"},
		{name: "json.RawMessage", val: json.RawMessage(obj), engineDeep: "v",
			bareDiffers: "the sample decodes the raw bytes into a map"},
		{name: "[]byte of JSON", val: []byte(obj), engineDeep: "v",
			bareDiffers: "JSON renders []byte as base64 text",
			deepDiffers: "the sample holds base64, which is not a document to descend into; " +
				"decoding it would mean sniffing content, which is how a LONGTEXT that happens to parse gets reshaped"},
		{name: "JSON array text", val: `[1,2,3]`},
		{name: "plain string", val: "hello"},
		{name: "float64", val: 1.5},
		{name: "bool", val: true},
		{name: "[]any list", val: []any{"a", "b"}},
		{name: "nested JSON in JSON", val: map[string]any{"inner": obj}},
		{name: "int64 small", val: int64(42),
			bareDiffers: "JSON has one number type; 42 comes back as float64"},
		{name: "int64 above 2^53", val: int64(9007199254740993),
			bareDiffers: "float64 cannot hold it -- the sample rounds it to 9007199254740992"},
		{name: "time.Time", val: time.Date(2026, 9, 23, 10, 30, 0, 0, time.UTC),
			bareDiffers: "JSON renders it as RFC3339 text"},
		{name: "[]byte non-JSON", val: []byte{0x01, 0x02},
			bareDiffers: "JSON renders []byte as base64 text"},
	}
}

func TestColumnShapeParityBetweenTheBuilderAndTheEngine(t *testing.T) {
	for _, shape := range columnShapes() {
		t.Run(shape.name, func(t *testing.T) {
			engineMsg := message.AcquireMessage()
			defer message.ReleaseMessage(engineMsg)
			engineMsg.SetOperation(hermod.Operation("insert"))
			engineMsg.SetData("col", shape.val)

			sample := engineMsg.ToMap()

			for _, sp := range []struct {
				spelling string
				differs  string
			}{
				{"{{.col}}", shape.bareDiffers},
				{"{{.after.col}}", ""},
				{"{{.col.k}}", shape.deepDiffers},
				{"{{.after.col.k}}", shape.deepDiffers},
			} {
				q := "SELECT " + sp.spelling
				_, builder := bindSample("pgx", q, sample)
				engine := sqlutil.TemplateArgsWith(q, sqlutil.Resolver(evaluator.MessageResolver(engineMsg)))

				agree := reflect.DeepEqual(builder, engine)
				switch {
				case sp.differs == "" && !agree:
					t.Errorf("%s: builder binds %#v, engine binds %#v -- these must agree",
						sp.spelling, builder[0], engine[0])
				case sp.differs != "" && agree:
					t.Errorf("%s: builder and engine now agree (%#v). The table says they cannot, because %s -- "+
						"if that is no longer true, drop the entry", sp.spelling, builder[0], sp.differs)
				}
			}

			if shape.engineDeep == nil {
				return
			}
			// Agreement is only half the guarantee: two sides that both resolve
			// to nothing agree perfectly and enrich nothing, which is exactly
			// the shape of the original report. A shape carrying a "k" has to
			// actually produce it.
			for _, spelling := range []string{"SELECT {{.col.k}}", "SELECT {{.after.col.k}}"} {
				got := sqlutil.TemplateArgsWith(spelling, sqlutil.Resolver(evaluator.MessageResolver(engineMsg)))[0]
				if !reflect.DeepEqual(got, shape.engineDeep) {
					t.Errorf("%s: engine binds %#v, want %#v", spelling, got, shape.engineDeep)
				}
			}
		})
	}
}

// An envelope path agrees for every shape, because both sides reach it through
// JSON. Stated on its own so the rule is a test rather than a pattern someone
// has to notice in the table above.
func TestAnEnvelopePathAgreesForEveryShape(t *testing.T) {
	for _, shape := range columnShapes() {
		engineMsg := message.AcquireMessage()
		engineMsg.SetOperation(hermod.Operation("insert"))
		engineMsg.SetData("col", shape.val)

		const q = "SELECT {{.after.col}}"
		_, builder := bindSample("pgx", q, engineMsg.ToMap())
		engine := sqlutil.TemplateArgsWith(q, sqlutil.Resolver(evaluator.MessageResolver(engineMsg)))

		if !reflect.DeepEqual(builder, engine) {
			t.Errorf("%s: builder %#v, engine %#v", shape.name, builder[0], engine[0])
		}
		message.ReleaseMessage(engineMsg)
	}
}

// A bare path is the only spelling that keeps the driver's Go type, which is
// what a key column needs. Pinned separately because the cost of getting it
// wrong is silent: a rounded bigint matches no row and reports no error.
func TestOnlyABarePathKeepsAnInt64Intact(t *testing.T) {
	const big = int64(9007199254740993) // 2^53 + 1

	engineMsg := message.AcquireMessage()
	defer message.ReleaseMessage(engineMsg)
	engineMsg.SetOperation(hermod.Operation("insert"))
	engineMsg.SetData("id", big)

	resolve := sqlutil.Resolver(evaluator.MessageResolver(engineMsg))

	if got := sqlutil.TemplateArgsWith("SELECT {{.id}}", resolve)[0]; got != any(big) {
		t.Errorf("{{.id}} = %#v, want the int64 unchanged", got)
	}
	if got := sqlutil.TemplateArgsWith("SELECT {{.after.id}}", resolve)[0]; got == any(big) {
		t.Error("{{.after.id}} kept the int64 -- if the envelope path no longer rounds, " +
			"say so here and in the db_lookup comment that warns against it")
	}
}
