package mcp

import (
	"bufio"
	"fmt"
	"io"
)

// maxMessageSize is the maximum allowed MCP message body size (4 MB).
const maxMessageSize = 4 * 1024 * 1024

// writeMessage writes one newline-delimited JSON-RPC message. MCP stdio uses
// one compact JSON object per line; Content-Length framing belongs to LSP and
// is not understood by servers built with the official MCP SDKs.
func writeMessage(w io.Writer, data []byte) error {
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// readMessage reads one newline-delimited JSON-RPC message from r and returns
// the raw JSON bytes without its line ending.
//
// r must be a *bufio.Reader. Callers that hold a plain io.Reader should
// wrap it once with bufio.NewReader and reuse that wrapper across
// multiple readMessage calls  --  creating a new bufio.Reader per call
// silently drops buffered data and breaks multi-message streams.
func readMessage(r io.Reader) ([]byte, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		// Caller passed a plain io.Reader without managing the
		// buffer  --  warn them once and wrap anyway. Multi-message
		// streams will lose data after the first read.
		br = bufio.NewReader(r)
	}
	return readMessageFromBufio(br)
}

func readMessageFromBufio(br *bufio.Reader) ([]byte, error) {
	message := make([]byte, 0, 4096)
	for {
		chunk, err := br.ReadSlice('\n')
		if len(message)+len(chunk) > maxMessageSize+1 {
			return nil, fmt.Errorf("message exceeds maximum %d bytes", maxMessageSize)
		}
		message = append(message, chunk...)
		if err == nil {
			break
		}
		if err != bufio.ErrBufferFull {
			if err == io.EOF && len(message) > 0 {
				return nil, fmt.Errorf("read message: missing newline: %w", io.ErrUnexpectedEOF)
			}
			return nil, fmt.Errorf("read message: %w", err)
		}
	}
	message = message[:len(message)-1]
	if len(message) > 0 && message[len(message)-1] == '\r' {
		message = message[:len(message)-1]
	}
	return message, nil
}
