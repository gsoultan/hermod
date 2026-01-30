package buffer

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/compression"
	"github.com/user/hermod/pkg/message"
)

const (
	encodingMagic     = 0x484D44 // "HMD"
	encodingThreshold = 1024     // 1KB
)

var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 4096)
	},
}

func encodeMessage(w io.Writer, msg hermod.Message, comp compression.Compressor) error {
	// Instead of many small writes, let's buffer in memory first
	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)

	tmp := buf[:0]

	// Helper to append length-prefixed data
	appendString := func(s string) {
		l := uint32(len(s))
		var lb [4]byte
		binary.LittleEndian.PutUint32(lb[:], l)
		tmp = append(tmp, lb[:]...)
		tmp = append(tmp, s...)
	}

	appendBytes := func(b []byte) {
		l := uint32(len(b))
		var lb [4]byte
		binary.LittleEndian.PutUint32(lb[:], l)
		tmp = append(tmp, lb[:]...)
		tmp = append(tmp, b...)
	}

	appendString(msg.ID())
	appendString(string(msg.Operation()))
	appendString(msg.Table())
	appendString(msg.Schema())
	appendBytes(msg.Before())
	appendBytes(msg.After())
	appendBytes(msg.Payload())

	metadata := msg.Metadata()
	var lb [4]byte
	binary.LittleEndian.PutUint32(lb[:], uint32(len(metadata)))
	tmp = append(tmp, lb[:]...)
	for k, v := range metadata {
		appendString(k)
		appendString(v)
	}

	dataToLowLevel := tmp
	algo := compression.None

	if comp != nil && len(tmp) > encodingThreshold {
		compressed, err := comp.Compress(tmp)
		if err == nil && len(compressed) < len(tmp) {
			dataToLowLevel = compressed
			algo = comp.Algorithm()
		}
	}

	// Write header: Magic (3 bytes) + Algo (1 byte) + UncompressedSize (4 bytes) + DataLength (4 bytes)
	var header [12]byte
	binary.LittleEndian.PutUint32(header[0:4], encodingMagic)
	header[3] = byte(algoToByte(algo))
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(tmp)))
	binary.LittleEndian.PutUint32(header[8:12], uint32(len(dataToLowLevel)))

	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(dataToLowLevel)
	return err
}

func algoToByte(algo compression.Algorithm) byte {
	switch algo {
	case compression.LZ4:
		return 1
	case compression.Snappy:
		return 2
	case compression.Zstd:
		return 3
	default:
		return 0
	}
}

func byteToAlgo(b byte) compression.Algorithm {
	switch b {
	case 1:
		return compression.LZ4
	case 2:
		return compression.Snappy
	case 3:
		return compression.Zstd
	default:
		return compression.None
	}
}

func decodeMessage(r io.Reader) (*message.DefaultMessage, error) {
	header := make([]byte, 12)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	magic := binary.LittleEndian.Uint32(header[0:4]) & 0xFFFFFF
	if magic != encodingMagic {
		return nil, fmt.Errorf("invalid magic number: %x", magic)
	}

	algoByte := header[3]
	uncompressedSize := binary.LittleEndian.Uint32(header[4:8])
	dataLength := binary.LittleEndian.Uint32(header[8:12])

	data := make([]byte, dataLength)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}

	if algoByte != 0 {
		algo := byteToAlgo(algoByte)
		comp, err := compression.NewCompressor(algo)
		if err != nil {
			return nil, err
		}
		decompressed, err := comp.Decompress(data)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress: %w", err)
		}
		if uint32(len(decompressed)) != uncompressedSize {
			return nil, fmt.Errorf("size mismatch: expected %d, got %d", uncompressedSize, len(decompressed))
		}
		data = decompressed
	}

	return decodeFromBytes(data)
}

func decodeFromBytes(data []byte) (*message.DefaultMessage, error) {
	msg := message.AcquireMessage()
	offset := 0

	readString := func() (string, error) {
		if offset+4 > len(data) {
			return "", io.ErrUnexpectedEOF
		}
		l := binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4
		if offset+int(l) > len(data) {
			return "", io.ErrUnexpectedEOF
		}
		s := string(data[offset : offset+int(l)])
		offset += int(l)
		return s, nil
	}

	readBytes := func() ([]byte, error) {
		if offset+4 > len(data) {
			return nil, io.ErrUnexpectedEOF
		}
		l := binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4
		if offset+int(l) > len(data) {
			return nil, io.ErrUnexpectedEOF
		}
		b := make([]byte, l)
		copy(b, data[offset:offset+int(l)])
		offset += int(l)
		return b, nil
	}

	id, err := readString()
	if err != nil {
		return nil, err
	}
	msg.SetID(id)

	op, err := readString()
	if err != nil {
		return nil, err
	}
	msg.SetOperation(hermod.Operation(op))

	table, err := readString()
	if err != nil {
		return nil, err
	}
	msg.SetTable(table)

	schema, err := readString()
	if err != nil {
		return nil, err
	}
	msg.SetSchema(schema)

	before, err := readBytes()
	if err != nil {
		return nil, err
	}
	msg.SetBefore(before)

	after, err := readBytes()
	if err != nil {
		return nil, err
	}
	msg.SetAfter(after)

	payload, err := readBytes()
	if err != nil {
		return nil, err
	}
	msg.SetPayload(payload)

	if offset+4 > len(data) {
		return nil, io.ErrUnexpectedEOF
	}
	metaCount := binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4
	for i := uint32(0); i < metaCount; i++ {
		k, err := readString()
		if err != nil {
			return nil, err
		}
		v, err := readString()
		if err != nil {
			return nil, err
		}
		msg.SetMetadata(k, v)
	}

	return msg, nil
}
