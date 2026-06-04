package filter

// Filter defines a generic interface for probabilistic data filters.
type Filter interface {
	Add(data []byte)
	Test(data []byte) bool
	Reset()
}
