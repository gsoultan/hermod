//go:build integration

package elasticsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/message"
)

// The Elasticsearch sink, against a real cluster.
//
// Elasticsearch was Beta: substantial and unit-tested, never shown to move a
// document. Two things are worth a cluster. The ordinary one — an index lands
// and a delete removes — and the bulk action line, which is assembled by
// printing values into JSON with %s rather than encoding them.
//
// That second one matters because the values are not the sink's. The document
// id is whatever the pipeline put there: a CDC primary key, a Kafka message
// key, a field out of a webhook body. Bulk is NDJSON — one action object per
// line — so an id carrying a quote or a newline does not merely corrupt its own
// request, it writes new action lines into the stream.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 ELASTICSEARCH_URL=http://127.0.0.1:9200 \
//	go test -tags=integration ./pkg/comm/sink/elasticsearch/

func requireES(t *testing.T) (string, string) {
	t.Helper()
	url := os.Getenv("ELASTICSEARCH_URL")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || url == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q ELASTICSEARCH_URL=%q in CI, where a cluster is "+
				"started for exactly this", os.Getenv("HERMOD_INTEGRATION"), url)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and ELASTICSEARCH_URL to run")
	}
	url = strings.TrimSuffix(url, "/")

	resp, err := http.Get(url + "/_cluster/health")
	if err != nil {
		t.Fatalf("Elasticsearch at %s is not reachable: %v", url, err)
	}
	_ = resp.Body.Close()

	index := "hermod_it_" + strings.ToLower(t.Name())
	drop := func() {
		req, _ := http.NewRequest(http.MethodDelete, url+"/"+index, nil)
		if r, err := http.DefaultClient.Do(req); err == nil {
			_ = r.Body.Close()
		}
	}
	drop()
	t.Cleanup(drop)
	return url, index
}

func esCount(t *testing.T, url, index string) int {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("%s/%s/_count", url, index))
	if err != nil {
		t.Fatalf("counting %s: %v", index, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return 0
	}
	var out struct {
		Count int `json:"count"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding count for %s: %s", index, body)
	}
	return out.Count
}

func esIndexExists(t *testing.T, url, index string) bool {
	t.Helper()
	req, _ := http.NewRequest(http.MethodHead, url+"/"+index, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HEAD %s: %v", index, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func newESMsg(t *testing.T, id string, op hermod.Operation, body string) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(m.Release)
	m.SetID(id)
	m.SetOperation(op)
	m.SetPayload([]byte(body))
	return m
}

// The ordinary path.
func TestADocumentIsIndexedAndDeleted(t *testing.T) {
	url, index := requireES(t)
	sink, err := NewElasticsearchSink([]string{url}, "", "", "", index, nil)
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	ctx := t.Context()
	if err := sink.WriteBatch(ctx, []hermod.Message{
		newESMsg(t, "a", hermod.OpCreate, `{"v":1}`),
		newESMsg(t, "b", hermod.OpCreate, `{"v":2}`),
	}); err != nil {
		t.Fatalf("index: %v", err)
	}
	if n := esCount(t, url, index); n != 2 {
		t.Fatalf("indexed %d documents, want 2", n)
	}

	if err := sink.WriteBatch(ctx, []hermod.Message{
		newESMsg(t, "a", hermod.OpDelete, ""),
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n := esCount(t, url, index); n != 1 {
		t.Errorf("after deleting one of two documents the index holds %d, want 1", n)
	}
}

// A document id is data, and it is printed into the bulk action line without
// being encoded.
//
// Bulk is NDJSON: one action object per line. An id containing a quote and a
// newline therefore does not corrupt only its own action — it closes that
// object and writes a further action line of the caller's choosing. Here that
// is a delete against a completely different index, which the sink was never
// asked to touch.
func TestADocumentIDCannotInjectBulkActions(t *testing.T) {
	url, index := requireES(t)
	victim := index + "_victim"

	// A document somebody else owns, which this sink has no business writing to.
	req, _ := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/%s/_doc/keepme?refresh=true", url, victim),
		strings.NewReader(`{"important":true}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("seeding the victim index: %v", err)
	}
	_ = resp.Body.Close()
	t.Cleanup(func() {
		r, _ := http.NewRequest(http.MethodDelete, url+"/"+victim, nil)
		if rr, err := http.DefaultClient.Do(r); err == nil {
			_ = rr.Body.Close()
		}
	})
	if n := esCount(t, url, victim); n != 1 {
		t.Fatalf("the victim index was not seeded; count=%d", n)
	}

	sink, err := NewElasticsearchSink([]string{url}, "", "", "", index, nil)
	if err != nil {
		t.Fatalf("sink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	// A *delete* action, deliberately. An index action is followed by a source
	// line, so anything injected after one is swallowed as the document body —
	// which is why the obvious version of this attack does nothing. A delete
	// action carries no source, so the parser reads the next line as another
	// action, and that is the line the id gets to write.
	hostile := fmt.Sprintf(`x" } }%s{ "delete" : { "_index" : "%s", "_id" : "keepme" } }`,
		"\n", victim)

	// The write may fail — malformed NDJSON is a perfectly good outcome. What
	// must not happen is the injected action taking effect.
	_ = sink.WriteBatch(t.Context(), []hermod.Message{
		newESMsg(t, hostile, hermod.OpDelete, ""),
	})

	// Give a successful bulk time to refresh.
	time.Sleep(500 * time.Millisecond)

	if n := esCount(t, url, victim); n != 1 {
		t.Errorf("a document id deleted %d document(s) from %s, an index this sink was "+
			"never pointed at\n"+
			"the bulk action line is built with fmt.Fprintf and %%s, so an id carrying a "+
			"quote and a newline closes the action object and writes further NDJSON lines "+
			"of its own — and a document id is whatever the pipeline put there: a CDC "+
			"primary key, a Kafka key, a field from a webhook body",
			1-n, victim)
	}
	_ = esIndexExists
	_ = context.Background
}
