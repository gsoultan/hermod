package compression

import (
	"bytes"
	"fmt"
	"io"

	"github.com/golang/snappy"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
)

type Algorithm string

const (
	None   Algorithm = ""
	LZ4    Algorithm = "lz4"
	Snappy Algorithm = "snappy"
	Zstd   Algorithm = "zstd"
)

type Compressor interface {
	Compress(data []byte) ([]byte, error)
	Decompress(data []byte) ([]byte, error)
	Algorithm() Algorithm
}

func NewCompressor(algo Algorithm) (Compressor, error) {
	switch algo {
	case None:
		return &noneCompressor{}, nil
	case LZ4:
		return &lz4Compressor{}, nil
	case Snappy:
		return &snappyCompressor{}, nil
	case Zstd:
		return &zstdCompressor{}, nil
	default:
		return nil, fmt.Errorf("unsupported compression algorithm: %s", algo)
	}
}

type noneCompressor struct{}

func (c *noneCompressor) Compress(data []byte) ([]byte, error)   { return data, nil }
func (c *noneCompressor) Decompress(data []byte) ([]byte, error) { return data, nil }
func (c *noneCompressor) Algorithm() Algorithm                   { return None }

type lz4Compressor struct{}

func (c *lz4Compressor) Compress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	var buf bytes.Buffer
	zw := lz4.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (c *lz4Compressor) Decompress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	zr := lz4.NewReader(bytes.NewReader(data))
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, zr); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (c *lz4Compressor) Algorithm() Algorithm { return LZ4 }

type snappyCompressor struct{}

func (c *snappyCompressor) Compress(data []byte) ([]byte, error) {
	return snappy.Encode(nil, data), nil
}

func (c *snappyCompressor) Decompress(data []byte) ([]byte, error) {
	return snappy.Decode(nil, data)
}

func (c *snappyCompressor) Algorithm() Algorithm { return Snappy }

type zstdCompressor struct{}

func (c *zstdCompressor) Compress(data []byte) ([]byte, error) {
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		return nil, err
	}
	return enc.EncodeAll(data, nil), nil
}

func (c *zstdCompressor) Decompress(data []byte) ([]byte, error) {
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	return dec.DecodeAll(data, nil)
}

func (c *zstdCompressor) Algorithm() Algorithm { return Zstd }
