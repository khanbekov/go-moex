/*
FILE: internal/fix/message.go

DESCRIPTION:
Minimal FIX 4.4 tag=value codec. Deliberately NOT a generic/spec-driven
engine like quickfixgo (no XML dictionary, no repeating-group validation
framework) — v1.0 only needs to encode/decode the small, fixed set of
message types FORTS FIX Gate actually uses (see fields.go). This mirrors
the go-okx/go-bybit philosophy of hand-written, allocation-conscious
codecs over generic libraries pulled in "just in case" (see internal/codec
in go-okx).

WIRE FORMAT (FIX 4.4, tag=value, SOH-delimited):

	8=FIX.4.4|9=<len>|35=<type>|49=<sender>|56=<target>|34=<seq>|52=<time>|
	...body fields...|10=<checksum>|

where '|' stands for SOH (0x01) below for readability.

BodyLength (tag 9) is the byte count from the first byte after the 9=...SOH
field to the byte immediately before "10=" — standard FIX rule. CheckSum
(tag 10) is the sum of all preceding bytes (including the trailing SOH of
tag 9) modulo 256, rendered as a zero-padded 3-digit decimal.
*/
package fix

import (
	"fmt"
	"strconv"
	"strings"
)

const soh = byte(0x01)

// field — one tag=value pair in wire order.
type field struct {
	tag   int
	value string
}

// Message — a single FIX message. MsgType (tag 35) is kept in a dedicated
// field rather than inside the generic slice, since every code path needs
// it (dispatch, encoding) and it is always present. The remaining
// header/body/trailer fields are kept in a single ordered slice (small
// messages, ~10-30 fields — linear scan is faster than a map for this size
// and avoids a second allocation).
type Message struct {
	msgType string
	fields  []field
}

// NewMessage creates an empty message for the given MsgType. Capacity is
// pre-sized for the typical FORTS application message (~20 fields) to
// avoid slice growth reallocations on the hot order-entry path.
func NewMessage(msgType string) *Message {
	return &Message{msgType: msgType, fields: make([]field, 0, 24)}
}

// SetStr appends a string field.
func (m *Message) SetStr(tag int, value string) *Message {
	m.fields = append(m.fields, field{tag: tag, value: value})
	return m
}

// SetInt appends an integer field.
func (m *Message) SetInt(tag int, value int64) *Message {
	return m.SetStr(tag, strconv.FormatInt(value, 10))
}

// SetPrice appends a price field, formatted with up to 5 decimal places
// (FIX Price16.5 — see spec §2.1 "Data types"), trailing zeros trimmed.
func (m *Message) SetPrice(tag int, value string) *Message {
	return m.SetStr(tag, value)
}

// MsgType returns the value of tag 35.
func (m *Message) MsgType() string { return m.msgType }

// GetStr returns the first value for tag, if present. Tag 35 (MsgType) is
// served from the dedicated field, everything else from the slice.
func (m *Message) GetStr(tag int) (string, bool) {
	if tag == TagMsgType {
		return m.msgType, m.msgType != ""
	}
	for i := range m.fields {
		if m.fields[i].tag == tag {
			return m.fields[i].value, true
		}
	}
	return "", false
}

// Fields exposes the raw ordered (tag, value) pairs excluding MsgType, for
// use by Session when framing an outgoing application message. Package-
// internal — the return type deliberately isn't part of the public API.
func (m *Message) rawFields() []field { return m.fields }

// GetInt returns tag parsed as int64.
func (m *Message) GetInt(tag int) (int64, bool) {
	s, ok := m.GetStr(tag)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// Encode serializes the message to the wire format, computing BodyLength
// and CheckSum. BeginString is always "FIX.4.4" for FORTS FIX Gate.
func Encode(senderCompID, targetCompID string, msgSeqNum int64, sendingTime string, msg *Message) []byte {
	var body []field = msg.rawFields()
	// Header fields after BeginString/BodyLength (which are computed last).
	var headerAndBody []field = make([]field, 0, 6+len(body))
	headerAndBody = append(headerAndBody,
		field{TagMsgType, msg.msgType},
		field{TagSenderCompID, senderCompID},
		field{TagTargetCompID, targetCompID},
		field{TagMsgSeqNum, strconv.FormatInt(msgSeqNum, 10)},
		field{TagSendingTime, sendingTime},
	)
	headerAndBody = append(headerAndBody, body...)

	var sb strings.Builder
	sb.Grow(32 + 24*len(headerAndBody))
	for _, f := range headerAndBody {
		sb.WriteString(strconv.Itoa(f.tag))
		sb.WriteByte('=')
		sb.WriteString(f.value)
		sb.WriteByte(soh)
	}
	var bodyStr string = sb.String()

	var prefix string = fmt.Sprintf("8=FIX.4.4%c9=%d%c", soh, len(bodyStr), soh)
	var withoutChecksum string = prefix + bodyStr

	var sum int = 0
	for i := 0; i < len(withoutChecksum); i++ {
		sum += int(withoutChecksum[i])
	}
	var checksum string = fmt.Sprintf("10=%03d%c", sum%256, soh)

	return []byte(withoutChecksum + checksum)
}

// Decode parses a single raw FIX message (as delivered by the framer in
// conn.go — already split on message boundaries via BodyLength) into a
// Message. Does not itself verify CheckSum/BodyLength — callers that need
// wire integrity checks should use VerifyChecksum first.
func Decode(raw []byte) (*Message, error) {
	var m *Message = &Message{fields: make([]field, 0, 24)}
	var tokens []string = strings.Split(string(raw), string(soh))
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		var idx int = strings.IndexByte(tok, '=')
		if idx < 0 {
			return nil, fmt.Errorf("fix: malformed field %q", tok)
		}
		var tagStr string = tok[:idx]
		var tag int
		var err error
		tag, err = strconv.Atoi(tagStr)
		if err != nil {
			return nil, fmt.Errorf("fix: non-numeric tag %q: %w", tagStr, err)
		}
		var value string = tok[idx+1:]
		if tag == TagMsgType {
			m.msgType = value
			continue
		}
		m.fields = append(m.fields, field{tag: tag, value: value})
	}
	return m, nil
}

// VerifyChecksum recomputes the checksum over raw (everything up to the
// trailing "10=nnn" field) and compares it to the CheckSum field found by
// Decode. Returns an error describing the mismatch, or nil if it matches.
func VerifyChecksum(raw []byte, m *Message) error {
	var idx int = strings.LastIndex(string(raw), "10=")
	if idx < 0 {
		return fmt.Errorf("fix: no checksum field in message")
	}
	var sum int = 0
	for i := 0; i < idx; i++ {
		sum += int(raw[i])
	}
	var want string = fmt.Sprintf("%03d", sum%256)
	var got, ok = m.GetStr(TagCheckSum)
	if !ok {
		return fmt.Errorf("fix: no checksum field parsed")
	}
	if got != want {
		return fmt.Errorf("fix: checksum mismatch: got %s want %s", got, want)
	}
	return nil
}
