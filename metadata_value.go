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
