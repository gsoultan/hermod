package service

import (
	"github.com/google/uuid"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// defaultSample is what a query binds against before the editor has a sample to
// offer. The seeded id keeps `{{.id}}` from binding NULL on the very first run,
// which reads as "the query matches nothing" rather than "there is no sample
// yet".
//
// It is seeded under "after" on purpose: this is a sample in the editor's
// ToMap() shape, and bindSample unwraps it exactly as the engine does.
func defaultSample() map[string]any {
	return map[string]any{"after": map[string]any{"id": uuid.NewString()}}
}

// bindSample turns a query and an editor sample into the statement and
// arguments the *engine* would produce for that sample.
//
// It goes through a real message rather than resolving against the sample map,
// and that is the whole point. The two shapes differ: ToMap() re-nests a CDC
// row's columns under "after", while the engine's Data() has them at the root
// because the data map is the after-image. Resolving against the map made the
// builder answer `{{.after.payload}}` with a row and the pipeline answer it with
// NULL -- a query an operator had watched succeed, silently enriching nothing.
//
// Building the message is what makes the agreement structural instead of
// imitated: message.PopulateFromMap is the same function the preview endpoint
// uses, and evaluator.MessageResolver is the same resolver db_lookup and
// execute_sql use.
// Releasing before the caller reads Args is safe, and not obviously so.
// ReleaseMessage -> Reset -> clear(m.data) empties the top-level map in place
// and returns the message to the pool, but a bound argument is always a value
// *inside* that map (a path always indexes into it), never the map itself, and
// clear does not touch the nested value. Arguments that came from the envelope
// go through msg.Payload(), which returns bytes.Clone, so they cannot alias the
// pooled buffer either. TestBindSampleResolvesTheAfterPrefix reads args after
// this function has returned, which is what keeps that true.
func bindSample(driver, query string, sample map[string]any) (string, []any) {
	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	message.PopulateFromMap(msg, sample)

	b := sqlutil.ParameterizeTemplateWith(driver, query, sqlutil.Resolver(evaluator.MessageResolver(msg)))
	return b.SQL, b.Args
}
