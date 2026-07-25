package mcp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// maxMessageSize is the maximum allowed MCP message body size (4 MB).
const maxMessageSize = 4 * 1024 * 1024

// writeMessage frames raw data with Content-Length headers and writes it to w.
//
// The MCP stdio transport uses the same HTTP-like header framing as LSP:
//
//	Content-Length: <length>\r\n
//	\r\n
//	<body>
func writeMessage(w io.Writer, data []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	_, err := io.WriteString(w, header)
	if err != nil {
		return err
	}
	if len(data) > 0 {
		_, err = w.Write(data)
	}
	return err
}

// readMessage reads one Content-Length framed message from r and returns
// the raw body bytes.
//
// r must be a *bufio.Reader. Callers that hold a plain io.Reader should
// wrap it once with bufio.NewReader and reuse that wrapper across
// multiple readMessage calls  --  creating a new bufio.Reader per call
// silently drops buffered data and breaks multi-message streams.
//
// The function parses HTTP-like headers terminated by an empty line
// (\r\n\r\n or \n\n), extracts Content-Length, and reads exactly that
// many bytes of body. Other headers (e.g. Content-Type) are accepted
// and ignored.
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
	var contentLength int
	contentLengthFound := false
	headerSectionEnd := false

	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read header: %w", err)
		}
		// Strip trailing \r\n or \n
		line = strings.TrimRight(line, "\r\n")

		if line == "" {
			// End of headers.
			headerSectionEnd = true
			break
		}

		if strings.HasPrefix(line, "Content-Length:") {
			val := strings.TrimSpace(line[len("Content-Length:"):])
			n, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length value %q: %w", val, err)
			}
			if n < 0 {
				return nil, fmt.Errorf("Content-Length %d is negative", n)
			}
			if n > maxMessageSize {
				return nil, fmt.Errorf("Content-Length %d exceeds maximum %d", n, maxMessageSize)
			}
			contentLength = n
			contentLengthFound = true
		}
		// Other headers (Content-Type, etc.) are ignored.
	}

	if !headerSectionEnd {
		return nil, errors.New("missing empty line after headers")
	}
	if !contentLengthFound {
		return nil, errors.New("missing Content-Length header")
	}

	body := make([]byte, contentLength)
	if contentLength > 0 {
		if _, err := io.ReadFull(br, body); err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
	}
	return body, nil
}
