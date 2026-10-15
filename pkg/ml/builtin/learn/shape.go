// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
