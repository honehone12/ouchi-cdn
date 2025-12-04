package ttlcache

import (
	"bytes"
	"compress/gzip"
	"io"
)

func compress(body io.ReadCloser) ([]byte, error) {
	defer body.Close()

	buff := new(bytes.Buffer)
	g := gzip.NewWriter(buff)
	defer g.Close()

	in, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	if _, err := g.Write(in); err != nil {
		return nil, err
	}
	if err := g.Flush(); err != nil {
		return nil, err
	}

	return buff.Bytes(), nil
}
