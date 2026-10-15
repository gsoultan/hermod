// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package hermod

// metadataReader is implemented by messages that can read one metadata entry
// without copying the map.
type metadataReader interface {
	MetadataValue(key string) (string, bool)
}

// MetadataValue reads one metadata entry, without copying the map when the
// message supports it.
//
// Metadata() has to clone — handing out the live map would race with a
// concurrent SetMetadata — so reading a single key through it copies every
// other entry only to discard them. Almost every caller wants one key:
// `_outbox_id`, `_source_node_id`, `traceparent`, the delivery markers. On a
// real workflow at 32 columns those reads were 11% of everything the pipeline
// allocated.
//
// A message that cannot do the single-key read still works, at the old cost.
// This lives here, beside the interface, because three packages had each grown
// a private copy of it and a fourth was about to.
func MetadataValue(msg Message, key string) (string, bool) {
	if msg == nil {
		return "", false
	}
	if r, ok := msg.(metadataReader); ok {
		return r.MetadataValue(key)
	}
	v, ok := msg.Metadata()[key]
	return v, ok
}
