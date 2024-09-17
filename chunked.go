package frogproxy

import (
	"io"
	"strconv"
)

type chunkedWriter struct {
	Wire io.Writer
}

func newChunkedWriter(w io.Writer) io.WriteCloser {
	return &chunkedWriter{w}
}

func (cw *chunkedWriter) Close() error {
	_, err := io.WriteString(cw.Wire, "0\r\n")
	return err
}

func (cw *chunkedWriter) Write(data []byte) (n int, err error) {
	if len(data) == 0 {
		return 0, nil
	}

	head := strconv.FormatInt(int64(len(data)), 16) + "\r\n"
	if _, err = io.WriteString(cw.Wire, head); err != nil {
		return 0, err
	}
	if n, err = cw.Wire.Write(data); err != nil {
		return
	}
	if n != len(data) {
		err = io.ErrShortWrite
		return
	}
	_, err = io.WriteString(cw.Wire, "\r\n")
	return
}
