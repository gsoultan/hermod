package buffer

import (
	"encoding/binary"
	"io"
	"sync"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 4096)
	},
}

func encodeMessage(w io.Writer, msg hermod.Message) error {
	// Instead of many small writes, let's buffer in memory first
	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)

	tmp := buf[:0]

	// Helper to append length-prefixed data
	appendString := func(s string) {
		l := uint32(len(s))
		tmp = append(tmp, 0, 0, 0, 0)
		binary.LittleEndian.PutUint32(tmp[len(tmp)-4:], l)
		tmp = append(tmp, s...)
	}

	appendBytes := func(b []byte) {
		l := uint32(len(b))
		tmp = append(tmp, 0, 0, 0, 0)
		binary.LittleEndian.PutUint32(tmp[len(tmp)-4:], l)
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
	tmp = append(tmp, 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(tmp[len(tmp)-4:], uint32(len(metadata)))
	for k, v := range metadata {
		appendString(k)
		appendString(v)
	}

	_, err := w.Write(tmp)
	return err
}

func decodeMessage(r io.Reader) (*message.DefaultMessage, error) {
	msg := message.AcquireMessage()

	header := make([]byte, 4)

	readString := func() (string, error) {
		if _, err := io.ReadFull(r, header); err != nil {
			return "", err
		}
		l := binary.LittleEndian.Uint32(header)
		buf := make([]byte, l)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", err
		}
		return string(buf), nil
	}

	readBytes := func() ([]byte, error) {
		if _, err := io.ReadFull(r, header); err != nil {
			return nil, err
		}
		l := binary.LittleEndian.Uint32(header)
		buf := make([]byte, l)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return buf, nil
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

	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	metaCount := binary.LittleEndian.Uint32(header)
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
