// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
