package learn

import (
	"errors"
	"fmt"
)

// shape checks the preprocessing and returns the number features, the
// encoded width, and each text feature's category count.
func (p *Prep) shape() (nNum, width int, catSize []int, err error) {
	if p == nil || len(p.Kinds) != len(p.Features) || len(p.Mean) != len(p.Features) ||
		len(p.Std) != len(p.Features) || len(p.Categories) != len(p.Features) {
		return 0, 0, nil, errors.New("its preprocessing does not match its features")
	}
	for j, k := range p.Kinds {
		switch k {
		case KindNumber:
			nNum++
			width++
		case KindString:
			catSize = append(catSize, len(p.Categories[j]))
			width += len(p.Categories[j])
		default:
			return 0, 0, nil, fmt.Errorf("feature kind %q", k)
		}
	}
	return nNum, width, catSize, nil
}
