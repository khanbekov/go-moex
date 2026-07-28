/*
FILE: internal/fix/conn.go

DESCRIPTION:
TCP framing for the FIX tag=value wire format: reads a raw byte stream and
yields complete, individually-parseable FIX messages by locating the
"9=<len>" (BodyLength) field and reading exactly that many body bytes plus
the trailing "10=nnn" checksum field. This is the standard FIX framing
algorithm (every engine — quickfixgo included — implements the same
scan-for-9-then-read-N-bytes approach; there is no alternative delimiter).
*/
package fix

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
)

// frameReader reads discrete FIX messages off a byte stream.
type frameReader struct {
	r *bufio.Reader
}

func newFrameReader(r io.Reader) *frameReader {
	return &frameReader{r: bufio.NewReaderSize(r, 64*1024)}
}

// ReadMessage blocks until one full FIX message has been read, returning
// its raw bytes (including "8=...SOH...10=nnnSOH"). Returns io.EOF (or a
// wrapped error) if the connection is closed mid-frame.
func (fr *frameReader) ReadMessage() ([]byte, error) {
	// 1. Read up to and including "8=FIX.4.4" + SOH.
	var beginField []byte
	var err error
	beginField, err = fr.r.ReadBytes(soh)
	if err != nil {
		return nil, err
	}
	if len(beginField) < 3 || beginField[0] != '8' || beginField[1] != '=' {
		return nil, fmt.Errorf("fix: frame does not start with tag 8 (BeginString): %q", beginField)
	}

	// 2. Read "9=<len>" + SOH.
	var bodyLenField []byte
	bodyLenField, err = fr.r.ReadBytes(soh)
	if err != nil {
		return nil, err
	}
	if len(bodyLenField) < 3 || bodyLenField[0] != '9' || bodyLenField[1] != '=' {
		return nil, fmt.Errorf("fix: expected tag 9 (BodyLength) after BeginString, got %q", bodyLenField)
	}
	var bodyLen int
	bodyLen, err = strconv.Atoi(string(bodyLenField[2 : len(bodyLenField)-1]))
	if err != nil {
		return nil, fmt.Errorf("fix: invalid BodyLength %q: %w", bodyLenField, err)
	}

	// 3. Read exactly bodyLen bytes (covers 35=...  through the field
	// immediately preceding "10=").
	var body []byte = make([]byte, bodyLen)
	_, err = io.ReadFull(fr.r, body)
	if err != nil {
		return nil, err
	}

	// 4. Read the trailing checksum field "10=nnn" + SOH.
	var checksumField []byte
	checksumField, err = fr.r.ReadBytes(soh)
	if err != nil {
		return nil, err
	}
	if len(checksumField) < 3 || checksumField[0] != '1' || checksumField[1] != '0' || checksumField[2] != '=' {
		return nil, fmt.Errorf("fix: expected tag 10 (CheckSum) after body, got %q", checksumField)
	}

	var out []byte = make([]byte, 0, len(beginField)+len(bodyLenField)+len(body)+len(checksumField))
	out = append(out, beginField...)
	out = append(out, bodyLenField...)
	out = append(out, body...)
	out = append(out, checksumField...)
	return out, nil
}
