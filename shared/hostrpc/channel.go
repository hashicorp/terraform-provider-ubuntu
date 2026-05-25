// Copyright IBM Corp. 2026

package hostrpc

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"

	"github.com/creachadair/jrpc2/channel"
)

var recordMagic = []byte{'T', 'F', 'N', '1'}

const (
	recordEncodingRaw  byte = 0
	recordEncodingGzip byte = 1
)

type compressedChannel struct {
	base      channel.Channel
	threshold int
}

func NewChannel(reader io.Reader, writer io.WriteCloser) channel.Channel {
	return NewCompressedChannel(channel.LSP(reader, writer), DefaultCompressionThreshold)
}

func NewCompressedChannel(base channel.Channel, threshold int) channel.Channel {
	if threshold <= 0 {
		threshold = DefaultCompressionThreshold
	}

	return &compressedChannel{
		base:      base,
		threshold: threshold,
	}
}

func (c *compressedChannel) Send(msg []byte) error {
	record, err := encodeRecord(msg, c.threshold)
	if err != nil {
		return err
	}

	return c.base.Send(record)
}

func (c *compressedChannel) Recv() ([]byte, error) {
	record, err := c.base.Recv()
	if err != nil {
		return nil, err
	}

	return decodeRecord(record)
}

func (c *compressedChannel) Close() error {
	return c.base.Close()
}

func encodeRecord(msg []byte, threshold int) ([]byte, error) {
	if len(msg) >= threshold {
		compressed, err := gzipRecord(msg)
		if err != nil {
			return nil, err
		}
		if len(compressed) < len(msg) {
			return wrapRecord(recordEncodingGzip, compressed), nil
		}
	}

	return wrapRecord(recordEncodingRaw, msg), nil
}

func decodeRecord(record []byte) ([]byte, error) {
	if len(record) < len(recordMagic)+1 || !bytes.Equal(record[:len(recordMagic)], recordMagic) {
		return record, nil
	}

	encoding := record[len(recordMagic)]
	payload := record[len(recordMagic)+1:]

	switch encoding {
	case recordEncodingRaw:
		return payload, nil
	case recordEncodingGzip:
		return gunzipRecord(payload)
	default:
		return nil, fmt.Errorf("unknown executor RPC record encoding %d", encoding)
	}
}

func wrapRecord(encoding byte, payload []byte) []byte {
	record := make([]byte, 0, len(recordMagic)+1+len(payload))
	record = append(record, recordMagic...)
	record = append(record, encoding)
	record = append(record, payload...)
	return record
}

func gzipRecord(msg []byte) ([]byte, error) {
	var buf bytes.Buffer

	writer, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, fmt.Errorf("create gzip writer: %w", err)
	}

	if _, err := writer.Write(msg); err != nil {
		writer.Close()
		return nil, fmt.Errorf("gzip record: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finalize gzip record: %w", err)
	}

	return buf.Bytes(), nil
}

func gunzipRecord(payload []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create gzip reader: %w", err)
	}
	defer reader.Close()

	decoded, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("gunzip record: %w", err)
	}

	return decoded, nil
}
