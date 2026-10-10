// Package geo holds the geometry transformer: the distance between two points
// on a record, and whether a point lies in a polygon. It does no geocoding and
// calls nothing outside the process.
package geo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	transformer.Register("geo", &Geo{})
}

// earthRadiusKm is the mean Earth radius (IUGG), the usual choice for the
// haversine formula. The sphere is an approximation: expect up to about 0.5%
// against an ellipsoidal distance.
const earthRadiusKm = 6371.0088

// kmPer converts a distance in km to the configured unit.
var kmPer = map[string]float64{
	"km": 1,
	"m":  1000,
	"mi": 1 / 1.609344,
}

// Geo computes a distance or a point-in-polygon test from coordinates on the
// record.
//
// Config:
//   - operation: "distance" (default) or "within".
//   - distance: lat1Field, lon1Field, lat2Field, lon2Field (paths to decimal
//     degrees) and unit ("km" default, "mi" or "m"). Writes the great-circle
//     distance to targetField, default "distance".
//   - within: latField, lonField and polygon, a GeoJSON Polygon or
//     MultiPolygon (or a Feature holding one) as text or an object. Holes
//     are honoured. Writes true or false to targetField, default "inside". A
//     point exactly on an edge may fall either way.
//
// A missing or out-of-range coordinate fails the record; it is not read as 0,
// which is a real place.
type Geo struct{}

func (g *Geo) Prepare(config map[string]any) (map[string]any, error) {
	if text, ok := config["polygon"].(string); ok {
		// A polygon that does not parse is reported by Transform.
		if polys, err := parsePolygon(text); err == nil {
			config[preparedPolygonKey] = preparedPolygon{text: text, polys: polys}
		}
	}
	return config, nil
}

func (g *Geo) Transform(_ context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	var err error
	switch op := strings.ToLower(strings.TrimSpace(core.GetConfigString(config, "operation"))); op {
	case "distance", "":
		err = distance(msg, config)
	case "within":
		err = within(msg, config)
	default:
		err = fmt.Errorf("operation %q is not distance or within", op)
	}
	if err != nil {
		return msg, fmt.Errorf("geo: %w", err)
	}
	return msg, nil
}

func distance(msg hermod.Message, config map[string]any) error {
	unit := strings.ToLower(strings.TrimSpace(core.GetConfigString(config, "unit")))
	if unit == "" {
		unit = "km"
	}
	factor, ok := kmPer[unit]
	if !ok {
		return fmt.Errorf("unit %q is not km, mi or m", unit)
	}
	lat1, lon1, err := point(msg, config, "lat1Field", "lon1Field")
	if err != nil {
		return err
	}
	lat2, lon2, err := point(msg, config, "lat2Field", "lon2Field")
	if err != nil {
		return err
	}
	msg.SetData(target(config, "distance"), haversineKm(lat1, lon1, lat2, lon2)*factor)
	return nil
}

func within(msg hermod.Message, config map[string]any) error {
	polys, err := polygonFor(config)
	if err != nil {
		return err
	}
	lat, lon, err := point(msg, config, "latField", "lonField")
	if err != nil {
		return err
	}
	msg.SetData(target(config, "inside"), polys.contains(lon, lat))
	return nil
}

func target(config map[string]any, def string) string {
	if t := strings.TrimSpace(core.GetConfigString(config, "targetField")); t != "" {
		return t
	}
	return def
}

// point reads one latitude/longitude pair from the fields the config names.
func point(msg hermod.Message, config map[string]any, latKey, lonKey string) (lat, lon float64, err error) {
	if lat, err = coordinate(msg, core.GetConfigString(config, latKey), latKey, 90); err != nil {
		return 0, 0, fmt.Errorf("latitude: %w", err)
	}
	if lon, err = coordinate(msg, core.GetConfigString(config, lonKey), lonKey, 180); err != nil {
		return 0, 0, fmt.Errorf("longitude: %w", err)
	}
	return lat, lon, nil
}

func coordinate(msg hermod.Message, path, key string, limit float64) (float64, error) {
	if strings.TrimSpace(path) == "" {
		return 0, fmt.Errorf("set %s", key)
	}
	raw := evaluator.GetMsgValByPath(msg, path)
	if raw == nil {
		return 0, fmt.Errorf("the record has no field %q", path)
	}
	v, ok := evaluator.ToFloat64(raw)
	if !ok || math.IsNaN(v) {
		return 0, fmt.Errorf("field %q holds %v, not a number", path, raw)
	}
	if math.Abs(v) > limit {
		return 0, fmt.Errorf("field %q holds %v, outside ±%v", path, v, limit)
	}
	return v, nil
}

// haversineKm is the great-circle distance between two points in degrees.
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusKm * math.Asin(math.Min(1, math.Sqrt(a)))
}

var errNoPolygon = errors.New("set polygon to a GeoJSON Polygon or MultiPolygon")
