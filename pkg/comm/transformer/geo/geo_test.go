package geo

import (
	"math"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
)

func newMsg(t *testing.T, fields map[string]any) hermod.Message {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	for k, v := range fields {
		msg.SetData(k, v)
	}
	return msg
}

func run(t *testing.T, msg hermod.Message, cfg map[string]any) (hermod.Message, error) {
	t.Helper()
	tr, ok := transformer.Get("geo")
	if !ok {
		t.Fatal("geo is not registered")
	}
	return tr.Transform(t.Context(), msg, cfg)
}

// London to Paris is about 343.5 km along the great circle.
var londonParis = map[string]any{
	"from": map[string]any{"lat": 51.5074, "lon": -0.1278},
	"to":   map[string]any{"lat": "48.8566", "lon": "2.3522"},
}

func distanceCfg(unit string) map[string]any {
	return map[string]any{
		"operation": "distance",
		"lat1Field": "from.lat", "lon1Field": "from.lon",
		"lat2Field": "to.lat", "lon2Field": "to.lon",
		"unit": unit, "targetField": "d",
	}
}

func TestDistance(t *testing.T) {
	tests := []struct {
		unit string
		want float64
	}{
		{"km", 343.56},
		{"", 343.56},
		{"mi", 213.48},
		{"m", 343556},
	}
	for _, tc := range tests {
		t.Run(tc.unit, func(t *testing.T) {
			out, err := run(t, newMsg(t, londonParis), distanceCfg(tc.unit))
			if err != nil {
				t.Fatalf("geo: %v", err)
			}
			got, ok := out.Data()["d"].(float64)
			if !ok {
				t.Fatalf("d = %#v, want a number", out.Data()["d"])
			}
			if math.Abs(got-tc.want)/tc.want > 0.001 {
				t.Errorf("distance = %v %s, want about %v", got, tc.unit, tc.want)
			}
		})
	}
}

// The editor opens on Distance without saving it, so no operation means
// distance.
func TestDistanceIsTheDefaultOperation(t *testing.T) {
	cfg := distanceCfg("km")
	delete(cfg, "operation")
	out, err := run(t, newMsg(t, londonParis), cfg)
	if err != nil {
		t.Fatalf("geo: %v", err)
	}
	if _, ok := out.Data()["d"].(float64); !ok {
		t.Errorf("d = %#v, want a distance", out.Data()["d"])
	}
}

func TestDistanceRefuses(t *testing.T) {
	tests := []struct {
		name    string
		fields  map[string]any
		cfg     map[string]any
		wantErr string
	}{
		{"a missing coordinate", map[string]any{"from": map[string]any{"lat": 1}}, distanceCfg("km"), "from.lon"},
		{"a latitude out of range", map[string]any{
			"from": map[string]any{"lat": 91, "lon": 0}, "to": map[string]any{"lat": 0, "lon": 0},
		}, distanceCfg("km"), "latitude"},
		{"an unknown unit", londonParis, distanceCfg("parsec"), "unit"},
		{"an unknown operation", londonParis, map[string]any{"operation": "geocode"}, "operation"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(t, newMsg(t, tc.fields), tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
}

// A square around central London with a hole cut out of its middle.
const squareWithHole = `{"type":"Polygon","coordinates":[
  [[-0.5,51.3],[0.3,51.3],[0.3,51.7],[-0.5,51.7],[-0.5,51.3]],
  [[-0.2,51.45],[0.0,51.45],[0.0,51.55],[-0.2,51.55],[-0.2,51.45]]
]}`

func withinCfg(polygon any) map[string]any {
	return map[string]any{
		"operation": "within", "latField": "lat", "lonField": "lon",
		"polygon": polygon, "targetField": "inside",
	}
}

func TestWithin(t *testing.T) {
	multi := `{"type":"Feature","geometry":{"type":"MultiPolygon","coordinates":[
	  [[[0,0],[1,0],[1,1],[0,1],[0,0]]],
	  [[[10,10],[11,10],[11,11],[10,11],[10,10]]]
	]}}`
	tests := []struct {
		name     string
		polygon  any
		lat, lon float64
		want     bool
	}{
		{"inside the square", squareWithHole, 51.35, -0.4, true},
		{"in the hole", squareWithHole, 51.5, -0.1, false},
		{"outside the square", squareWithHole, 52.0, -0.1, false},
		{"in the second polygon of a multipolygon feature", multi, 10.5, 10.5, true},
		{"between the polygons", multi, 5, 5, false},
		{"polygon given as an object", map[string]any{
			"type": "Polygon", "coordinates": []any{[]any{
				[]any{0.0, 0.0}, []any{2.0, 0.0}, []any{2.0, 2.0}, []any{0.0, 2.0}, []any{0.0, 0.0},
			}},
		}, 1, 1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := run(t, newMsg(t, map[string]any{"lat": tc.lat, "lon": tc.lon}), withinCfg(tc.polygon))
			if err != nil {
				t.Fatalf("geo: %v", err)
			}
			if got := out.Data()["inside"]; got != tc.want {
				t.Errorf("inside = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWithinRefusesABadPolygon(t *testing.T) {
	tests := []struct {
		name, polygon, wantErr string
	}{
		{"not json", `{"type":`, "GeoJSON"},
		{"a point", `{"type":"Point","coordinates":[0,0]}`, "Polygon"},
		{"a ring of two points", `{"type":"Polygon","coordinates":[[[0,0],[1,1]]]}`, "ring"},
		{"no polygon", ``, "polygon"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(t, newMsg(t, map[string]any{"lat": 0, "lon": 0}), withinCfg(tc.polygon))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
}

func TestPreparedPolygonFollowsTheConfig(t *testing.T) {
	tr, _ := transformer.Get("geo")
	pt, ok := tr.(transformer.PreparedTransformer)
	if !ok {
		t.Fatal("geo does not prepare its polygon")
	}
	cfg, err := pt.Prepare(withinCfg(squareWithHole))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	cfg["polygon"] = `{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,1],[0,0]]]}`
	out, err := tr.Transform(t.Context(), newMsg(t, map[string]any{"lat": 0.5, "lon": 0.5}), cfg)
	if err != nil {
		t.Fatalf("geo: %v", err)
	}
	if out.Data()["inside"] != true {
		t.Error("the polygon prepared before the edit was used")
	}
}
