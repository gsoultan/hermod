package grpc

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/proto"
)

func TestRecommendCarriesARecommendationID(t *testing.T) {
	c, key := dial(t, asRecommender(t))
	resp, err := c.Recommend(withKey(key), &proto.RecommendRequest{Vhost: "a", Model: "double",
		Subject: &proto.RecommendRequest_User{User: "1000"}, K: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resp.GetRecommendationId(), "rec_") {
		t.Errorf("recommendation_id = %q", resp.GetRecommendationId())
	}
}

func TestRecommendScenarioAnswersAVisitPlan(t *testing.T) {
	c, key := dial(t, servingFixture(t, filepath.Join("scenario", "visits.rec")))
	resp, err := c.RecommendScenario(withKey(key), &proto.RecommendScenarioRequest{Vhost: "a", Model: "double",
		Subject: &proto.ScenarioSubject{Salesman: "R03"}, PeriodFrom: "2026-10-05", PeriodTo: "2026-10-09", K: 4})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetModel() != "double" || !strings.HasPrefix(resp.GetRecommendationId(), "rec_") || len(resp.GetResults()) != 1 {
		t.Fatalf("response = %v", resp)
	}
	res := resp.GetResults()[0]
	if res.GetSubject() != "R03" || len(res.GetItems()) != 4 || len(res.GetPlan()) == 0 || res.GetPlan()[0].GetDate() != "2026-10-05" {
		t.Fatalf("result = %v", res)
	}
	first := res.GetItems()[0]
	if first.GetId() == "" || first.GetRank() != 1 || len(first.GetReasons()) == 0 || first.GetReasons()[0].GetCode() == "" {
		t.Errorf("first item = %v", first)
	}
}

// within_days and all_customers reach a reorder model, and its answer
// carries due dates, overdue marks and the products due.
func TestRecommendScenarioWithinDays(t *testing.T) {
	c, key := dial(t, servingFixture(t, filepath.Join("scenario", "reorder.rec")))
	resp, err := c.RecommendScenario(withKey(key), &proto.RecommendScenarioRequest{Vhost: "a", Model: "double",
		Subject: &proto.ScenarioSubject{AllCustomers: true}, WithinDays: 30, K: 500})
	if err != nil {
		t.Fatal(err)
	}
	res := resp.GetResults()[0]
	if res.GetSubject() != "all_customers" || len(res.GetItems()) == 0 {
		t.Fatalf("result = %v", res)
	}
	end := time.Now().UTC().AddDate(0, 0, 30).Format(time.DateOnly)
	today := time.Now().UTC().Format(time.DateOnly)
	products := 0
	for _, p := range res.GetItems() {
		if p.GetDueDate() == "" || p.GetDueDate() >= end || p.GetOverdue() != (p.GetDueDate() < today) {
			t.Errorf("%s: due %q, overdue %v", p.GetId(), p.GetDueDate(), p.GetOverdue())
		}
		for _, d := range p.GetProducts() {
			products++
			if d.GetId() == "" || d.GetDueDate() == "" || d.GetFields().AsMap()["name"] == nil {
				t.Errorf("%s: product %v", p.GetId(), d)
			}
		}
	}
	if products == 0 {
		t.Error("no product due")
	}
	_, err = c.RecommendScenario(withKey(key), &proto.RecommendScenarioRequest{Vhost: "a", Model: "double",
		Subject: &proto.ScenarioSubject{Salesman: "R01"}, WithinDays: 400})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("400 days: %v", err)
	}
}

func TestRecommendScenarioRefusals(t *testing.T) {
	c, key := dial(t, servingFixture(t, filepath.Join("scenario", "visits.rec")))
	pin, err := structpb.NewStruct(map[string]any{"kind": "pin"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		key  string
		req  *proto.RecommendScenarioRequest
		code codes.Code
	}{
		{"no key", "", &proto.RecommendScenarioRequest{Subject: &proto.ScenarioSubject{Salesman: "R03"}}, codes.Unauthenticated},
		{"the wrong subject", key, &proto.RecommendScenarioRequest{Subject: &proto.ScenarioSubject{Customer: "C0001"}}, codes.InvalidArgument},
		{"no subject", key, &proto.RecommendScenarioRequest{}, codes.InvalidArgument},
		{"a bad period", key, &proto.RecommendScenarioRequest{Subject: &proto.ScenarioSubject{Salesman: "R03"}, PeriodFrom: "monday"}, codes.InvalidArgument},
		{"a bad rule", key, &proto.RecommendScenarioRequest{Subject: &proto.ScenarioSubject{Salesman: "R03"}, Rules: []*structpb.Struct{pin}}, codes.InvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.Vhost, tc.req.Model = "a", "double"
			_, err := c.RecommendScenario(withKey(tc.key), tc.req)
			if status.Code(err) != tc.code {
				t.Errorf("err = %v, want %v", err, tc.code)
			}
		})
	}
}

// A format 1 model answers scenario calls as a products model: a customer's
// items, a salesman refused as the wrong subject.
func TestRecommendScenarioServesAFormat1ModelAsProducts(t *testing.T) {
	c, key := dial(t, asRecommender(t))
	resp, err := c.RecommendScenario(withKey(key), &proto.RecommendScenarioRequest{Vhost: "a", Model: "double",
		Subject: &proto.ScenarioSubject{Customer: "1000"}, K: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetResults()) != 1 || len(resp.GetResults()[0].GetItems()) != 3 {
		t.Fatalf("response = %v", resp)
	}
	_, err = c.RecommendScenario(withKey(key), &proto.RecommendScenarioRequest{Vhost: "a", Model: "double",
		Subject: &proto.ScenarioSubject{Salesman: "R1"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("a salesman: %v", err)
	}
}

// A scenario call sent async over REST with a serving key is read over gRPC
// too, with GetJob: its result is the RecommendScenario answer, and a
// recommendation job's carries its recommendation_id.
func TestARecommendationJobIsReadAsTheCallItStandsFor(t *testing.T) {
	pj := &proto.Job{Kind: storage.MLJobRecommend, Model: "plan"}
	raw := `{"recommendation_id":"rec_a","results":[{"for":"R03",` +
		`"items":[{"id":"C1","score":0.5,"rank":1,"reasons":[{"code":"overdue","text":"Overdue"}],"fields":{"name":"Customer 1"}}],` +
		`"plan":[{"date":"2026-10-05","items":[{"id":"C1","score":0.5,"rank":1}]}]}]}`
	if err := setResult(pj, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	got := pj.GetRecommendScenarioResult()
	if got.GetModel() != "plan" || got.GetRecommendationId() != "rec_a" || len(got.GetResults()) != 1 {
		t.Fatalf("result = %v", pj.GetResult())
	}
	res := got.GetResults()[0]
	if res.GetSubject() != "R03" || len(res.GetItems()) != 1 || res.GetItems()[0].GetReasons()[0].GetCode() != "overdue" ||
		res.GetItems()[0].GetFields().AsMap()["name"] != "Customer 1" || res.GetPlan()[0].GetDate() != "2026-10-05" {
		t.Errorf("result = %v", res)
	}

	pj = &proto.Job{Kind: storage.MLJobRecommend, Model: "recs"}
	if err := setResult(pj, []byte(`{"model":"recs","items":[{"item":"S1","score":1}],"recommendation_id":"rec_b"}`)); err != nil {
		t.Fatal(err)
	}
	if r := pj.GetRecommendResult(); len(r.GetItems()) != 1 || r.GetRecommendationId() != "rec_b" {
		t.Errorf("result = %v", pj.GetResult())
	}
}

// segment and size reach the model: only that category and size of
// customers are listed, each carrying both in its fields.
func TestRecommendScenarioKeepsACategoryAndSize(t *testing.T) {
	c, key := dial(t, servingFixture(t, filepath.Join("scenario", "reorder_peers.rec")))
	resp, err := c.RecommendScenario(withKey(key), &proto.RecommendScenarioRequest{Vhost: "a", Model: "double",
		Subject: &proto.ScenarioSubject{AllCustomers: true, Segment: "a", Size: "Large"}, WithinDays: 30, K: 500})
	if err != nil {
		t.Fatal(err)
	}
	items := resp.GetResults()[0].GetItems()
	if len(items) == 0 {
		t.Fatal("no large A customers")
	}
	for _, p := range items {
		if f := p.GetFields().AsMap(); f["segment"] != "A" || f["size"] != "large" {
			t.Errorf("%s: fields %v", p.GetId(), f)
		}
	}
	// A customer of another category is not listed.
	other, err := c.RecommendScenario(withKey(key), &proto.RecommendScenarioRequest{Vhost: "a", Model: "double",
		Subject: &proto.ScenarioSubject{Customer: items[0].GetId(), Segment: "B"}})
	if err != nil || len(other.GetResults()[0].GetItems()) != 0 {
		t.Errorf("an A customer asked for B: %v, %v", other, err)
	}
}

func TestRecommendScenarioSaysWhatNearbyCustomersBuy(t *testing.T) {
	c, key := dial(t, servingFixture(t, filepath.Join("scenario", "products_near.rec")))
	resp, err := c.RecommendScenario(withKey(key), &proto.RecommendScenarioRequest{Vhost: "a", Model: "double",
		Subject: &proto.ScenarioSubject{Customer: "A16"}, K: 5})
	if err != nil {
		t.Fatal(err)
	}
	if s := resp.GetResults()[0].GetSummary(); !strings.HasPrefix(s, "Minimarket customers within 5 km buy: ") {
		t.Errorf("summary %q", s)
	}
}

// brand, subcategory and stock reach the model: only that brand's products
// are listed, each carrying its product details, and one given as out of
// stock is not.
func TestRecommendScenarioKeepsABrandAndTakesStock(t *testing.T) {
	c, key := dial(t, servingFixture(t, filepath.Join("scenario", "products_details.rec")))
	resp, err := c.RecommendScenario(withKey(key), &proto.RecommendScenarioRequest{Vhost: "a", Model: "double",
		Subject: &proto.ScenarioSubject{Customer: "C0001", Brand: "cahaya", Subcategory: "Dairy Basic"}, K: 10})
	if err != nil {
		t.Fatal(err)
	}
	items := resp.GetResults()[0].GetItems()
	if len(items) == 0 {
		t.Fatal("no Cahaya dairy basic products")
	}
	for _, p := range items {
		if f := p.GetFields().AsMap(); f["brand"] != "Cahaya" || f["subcategory"] != "dairy basic" || f["unit"] == nil || f["pack_size"] == nil {
			t.Errorf("%s: fields %v", p.GetId(), f)
		}
	}
	gone := items[0].GetId()
	resp, err = c.RecommendScenario(withKey(key), &proto.RecommendScenarioRequest{Vhost: "a", Model: "double",
		Subject: &proto.ScenarioSubject{Customer: "C0001", Brand: "cahaya", Subcategory: "Dairy Basic"}, K: 10,
		Stock: map[string]float64{gone: 0}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range resp.GetResults()[0].GetItems() {
		if p.GetId() == gone {
			t.Errorf("%s is out of stock and still recommended", gone)
		}
	}
}

// price_tier, channel and region reach the model: only those customers are
// listed, each carrying all three in its fields.
func TestRecommendScenarioKeepsAPriceTierChannelAndRegion(t *testing.T) {
	c, key := dial(t, servingFixture(t, filepath.Join("scenario", "audience_market.rec")))
	resp, err := c.RecommendScenario(withKey(key), &proto.RecommendScenarioRequest{Vhost: "a", Model: "double",
		Subject: &proto.ScenarioSubject{Product: "S1", PriceTier: "gold", Channel: "retail", Region: "N"}, K: 50})
	if err != nil {
		t.Fatal(err)
	}
	items := resp.GetResults()[0].GetItems()
	if len(items) == 0 {
		t.Fatal("no Gold Retail customers in N")
	}
	for _, p := range items {
		if f := p.GetFields().AsMap(); f["price_tier"] != "Gold" || f["channel"] != "Retail" || f["region"] != "N" {
			t.Errorf("%s: fields %v", p.GetId(), f)
		}
	}
}
