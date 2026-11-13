// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package compression

import (
	"bytes"
	"errors"
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

// ErrTooLarge is returned by DecompressLimit when the output would pass the
// limit.
var ErrTooLarge = errors.New("decompressed data is larger than the limit")

// DecompressLimit undoes algo on data, refusing with ErrTooLarge once the
// output would pass limit bytes. Use it for anything that arrives from
// outside: Decompress inflates whatever it is handed, and a few hundred bytes
// of zstd can declare gigabytes. The output is read through a limit rather
// than trusted from a header, except for snappy, whose block format states its
// decoded length up front and decodes in one allocation of that length.
func DecompressLimit(algo Algorithm, data []byte, limit int64) ([]byte, error) {
	switch algo {
	case None:
		if int64(len(data)) > limit {
			return nil, ErrTooLarge
		}
		return data, nil
	case LZ4:
		if len(data) == 0 {
			return data, nil
		}
		return readLimit(lz4.NewReader(bytes.NewReader(data)), limit)
	case Snappy:
		n, err := snappy.DecodedLen(data)
		if err != nil {
			return nil, err
		}
		if int64(n) > limit {
			return nil, ErrTooLarge
		}
		return snappy.Decode(nil, data)
	case Zstd:
		dec, err := zstd.NewReader(bytes.NewReader(data),
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderMaxMemory(uint64(limit)+1))
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		out, err := readLimit(dec, limit)
		if errors.Is(err, zstd.ErrDecoderSizeExceeded) || errors.Is(err, zstd.ErrWindowSizeExceeded) {
			return nil, ErrTooLarge
		}
		return out, err
	default:
		return nil, fmt.Errorf("unsupported compression algorithm: %s", algo)
	}
}

// readLimit reads r to its end, or fails with ErrTooLarge at limit+1 bytes.
func readLimit(r io.Reader, limit int64) ([]byte, error) {
	out, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > limit {
		return nil, ErrTooLarge
	}
	return out, nil
}
