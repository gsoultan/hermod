package geo

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// maxPolygonVertices bounds the polygon a node may hold. Every record is
// tested against every vertex, so the bound is on per-record cost as much as
// on memory.
const maxPolygonVertices = 100_000

const preparedPolygonKey = "_parsed_polygon"

// preparedPolygon is a parsed polygon with the text it came from, so a config
// edited after Prepare is parsed again rather than ignored.
type preparedPolygon struct {
	text  string
	polys polygons
}

// ring is a closed line of [lon, lat] points.
type ring [][2]float64

// polygon is an outer ring followed by its holes.
type polygon []ring

type polygons []polygon

// contains reports whether (x, y) lies in any polygon: inside its outer ring
// and inside none of its holes.
func (ps polygons) contains(x, y float64) bool {
	for _, p := range ps {
		if !p[0].contains(x, y) {
			continue
		}
		inHole := false
		for _, hole := range p[1:] {
			if hole.contains(x, y) {
				inHole = true
				break
			}
		}
		if !inHole {
			return true
		}
	}
	return false
}

// contains is the even-odd ray-casting test on planar lon/lat, which is what
// GeoJSON's coordinates are. It does not model edges that cross the
// antimeridian.
func (r ring) contains(x, y float64) bool {
	in := false
	for i, j := 0, len(r)-1; i < len(r); j, i = i, i+1 {
		xi, yi := r[i][0], r[i][1]
		xj, yj := r[j][0], r[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}

func polygonFor(config map[string]any) (polygons, error) {
	switch v := config["polygon"].(type) {
	case string:
		if p, ok := config[preparedPolygonKey].(preparedPolygon); ok && p.text == v {
			return p.polys, nil
		}
		return parsePolygon(v)
	case map[string]any:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("polygon: %w", err)
		}
		return parsePolygon(string(b))
	case nil:
		return nil, errNoPolygon
	default:
		return nil, fmt.Errorf("polygon must be GeoJSON, not %T", v)
	}
}

// geoJSON is the subset of a GeoJSON object a polygon is read from.
type geoJSON struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
	Geometry    *geoJSON        `json:"geometry"`
}

func parsePolygon(text string) (polygons, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errNoPolygon
	}
	var g geoJSON
	if err := json.Unmarshal([]byte(text), &g); err != nil {
		return nil, fmt.Errorf("polygon is not valid GeoJSON: %w", err)
	}
	if g.Type == "Feature" && g.Geometry != nil {
		g = *g.Geometry
	}
	var ps polygons
	switch g.Type {
	case "Polygon":
		var p [][][]float64
		if err := json.Unmarshal(g.Coordinates, &p); err != nil {
			return nil, fmt.Errorf("polygon coordinates: %w", err)
		}
		ps = append(ps, toPolygon(p))
	case "MultiPolygon":
		var mp [][][][]float64
		if err := json.Unmarshal(g.Coordinates, &mp); err != nil {
			return nil, fmt.Errorf("multipolygon coordinates: %w", err)
		}
		for _, p := range mp {
			ps = append(ps, toPolygon(p))
		}
	case "":
		return nil, errors.New("polygon is not GeoJSON: it has no type")
	default:
		return nil, fmt.Errorf("polygon is a GeoJSON %q; use a Polygon or MultiPolygon", g.Type)
	}
	return ps, validate(ps)
}

func toPolygon(coords [][][]float64) polygon {
	p := make(polygon, 0, len(coords))
	for _, rc := range coords {
		r := make(ring, 0, len(rc))
		for _, pt := range rc {
			// A position short of [lon, lat] is dropped, so its ring comes
			// up short and validate names it, rather than becoming (0, 0).
			if len(pt) >= 2 {
				r = append(r, [2]float64{pt[0], pt[1]})
			}
		}
		p = append(p, r)
	}
	return p
}

// validate checks what GeoJSON requires of a ring -- at least four positions
// -- and bounds the total size.
func validate(ps polygons) error {
	vertices := 0
	for _, p := range ps {
		if len(p) == 0 {
			return errors.New("a polygon has no rings")
		}
		for _, r := range p {
			if len(r) < 4 {
				return fmt.Errorf("a polygon ring has %d positions; GeoJSON needs at least 4", len(r))
			}
			vertices += len(r)
		}
	}
	if vertices > maxPolygonVertices {
		return fmt.Errorf("the polygon has %d vertices, above the %d allowed", vertices, maxPolygonVertices)
	}
	return nil
}
