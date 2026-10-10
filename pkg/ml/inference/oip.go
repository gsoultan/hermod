package inference

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// tensor is one named input or output of the Open Inference Protocol. Data is
// flat, in row-major order.
type tensor struct {
	Name     string `json:"name"`
	Shape    []int  `json:"shape"`
	Datatype string `json:"datatype"`
	Data     []any  `json:"data"`
}

// predictOIP calls an Open Inference Protocol server.
func (c *Client) predictOIP(ctx context.Context, t Target, rows []Row) ([]Row, error) {
	var (
		inputs []tensor
		err    error
	)
	if t.InputName != "" {
		inputs, err = matrixInput(t, rows)
	} else {
		inputs = columnInputs(rows)
	}
	if err != nil {
		return nil, err
	}

	endpoint := strings.TrimRight(t.URL, "/") + "/v2/models/" + url.PathEscape(t.Model)
	if t.Version != "" {
		endpoint += "/versions/" + url.PathEscape(t.Version)
	}
	endpoint += "/infer"

	var reply struct {
		Outputs []tensor `json:"outputs"`
	}
	if err := c.post(ctx, t, endpoint, map[string]any{"inputs": inputs}, &reply); err != nil {
		return nil, err
	}
	return outputRows(reply.Outputs, len(rows))
}

// columnInputs sends each field as its own tensor of shape [rows]. The fields
// are the union over all rows, sorted so the request is the same every time; a
// row without a field sends null for it.
func columnInputs(rows []Row) []tensor {
	var names []string
	for _, r := range rows {
		for k := range r {
			if !slices.Contains(names, k) {
				names = append(names, k)
			}
		}
	}
	slices.Sort(names)

	inputs := make([]tensor, 0, len(names))
	for _, name := range names {
		data := make([]any, len(rows))
		for i, r := range rows {
			data[i] = r[name]
		}
		inputs = append(inputs, tensor{Name: name, Shape: []int{len(rows)}, Datatype: datatypeOf(data), Data: data})
	}
	return inputs
}

// datatypeOf picks the tensor type for a column: FP64 if every value is a
// number, BOOL if every value is a boolean, BYTES otherwise. JSON gives every
// number as a float64, so integers travel as FP64 too.
func datatypeOf(data []any) string {
	allNum, allBool := true, true
	for _, v := range data {
		switch v.(type) {
		case float64, float32, int, int64, int32:
			allBool = false
		case bool:
			allNum = false
		default:
			allNum, allBool = false, false
		}
	}
	switch {
	case allNum:
		return "FP64"
	case allBool:
		return "BOOL"
	default:
		return "BYTES"
	}
}

// matrixInput builds the one FP32 tensor [rows, features] that a typical
// exported model takes. A boolean is sent as 1 or 0; anything that is not a
// number is refused, naming the field, rather than sent as a guess.
func matrixInput(t Target, rows []Row) ([]tensor, error) {
	data := make([]any, 0, len(rows)*len(t.Features))
	for i, r := range rows {
		for _, f := range t.Features {
			v, ok := r[f]
			if !ok || v == nil {
				return nil, fmt.Errorf("row %d has no value for feature %q", i, f)
			}
			n, ok := toFloat(v)
			if !ok {
				return nil, fmt.Errorf("row %d: feature %q is %T, not a number", i, f, v)
			}
			data = append(data, n)
		}
	}
	return []tensor{{Name: t.InputName, Shape: []int{len(rows), len(t.Features)}, Datatype: "FP32", Data: data}}, nil
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// outputRows splits each output tensor back into rows. A tensor of shape [n]
// gives each row one value; [n, k, ...] gives each row a list of the rest.
func outputRows(outputs []tensor, n int) ([]Row, error) {
	if len(outputs) == 0 {
		return nil, errors.New("the model server returned no outputs")
	}
	rows := make([]Row, n)
	for i := range rows {
		rows[i] = Row{}
	}
	for _, out := range outputs {
		if len(out.Shape) == 0 || out.Shape[0] != n {
			return nil, fmt.Errorf("output %q has shape %v; expected %d rows", out.Name, out.Shape, n)
		}
		per := 1
		for _, d := range out.Shape[1:] {
			per *= d
		}
		if len(out.Data) != n*per {
			return nil, fmt.Errorf("output %q has %d values; shape %v needs %d", out.Name, len(out.Data), out.Shape, n*per)
		}
		for i := range n {
			if len(out.Shape) == 1 {
				rows[i][out.Name] = out.Data[i]
				continue
			}
			rows[i][out.Name] = slices.Clone(out.Data[i*per : (i+1)*per])
		}
	}
	return rows, nil
}
