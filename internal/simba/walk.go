/*
FILE: internal/simba/walk.go

DESCRIPTION:
Iteration over the SBE messages inside one SIMBA packet. Spec §2.3.1: an
Incremental packet carries "one or more SBE messages" (a Snapshot packet
carries exactly one), and MOEX's reference PythonSimbaClient loops
`while offset < len(data)` — so a decoder that reads only the first
message silently drops the rest of every multi-order transaction.

To step from one message to the next the walker must know each template's
wire "shape": root block (BlockLength from the SBE header) + repeating
groups (groupSize/groupSize2 dimensions + blockLength × numInGroup) +
trailing var-data fields (uint16 length + bytes). Shapes below are taken
from simba_spectra-9.0.xml; templates with shapes the walker cannot size
(DiscreteAuction: var-data nested inside a group) are only accepted as the
last message of a packet.
*/
package simba

import (
	"encoding/binary"
	"fmt"
)

// templateShape — how to skip a message body after its root block.
type templateShape struct {
	groupDims []int // per repeating group: 3 for groupSize (uint16+uint8), 4 for groupSize2 (uint16+uint16).
	varData   int   // number of trailing var-length fields (uint16 length prefix each).
	unsizable bool  // shape has data nested inside a group; cannot be walked past.
}

var templateShapes = map[uint16]templateShape{
	TemplateHeartbeat:                {},
	TemplateSequenceReset:            {},
	TemplateEmptyBook:                {},
	TemplateBestPrices:               {groupDims: []int{3}},
	TemplateOrderUpdate:              {},
	TemplateOrderExecution:           {},
	TemplateOrderBookSnapshot:        {groupDims: []int{3}},
	TemplateSecurityDefinition:       {groupDims: []int{3, 3, 3, 3, 3}, varData: 2},
	TemplateSecurityStatus:           {},
	TemplateSecurityDefinitionUpdate: {},
	TemplateTradingSessionStatus:     {},
	TemplateDiscreteAuction:          {unsizable: true},
	TemplateSecurityMassStatus:       {groupDims: []int{4}},
	TemplateSecurityGroupStatus:      {},
	TemplateLogon:                    {},
	TemplateLogout:                   {},
	TemplateMarketDataRequest:        {},
	TemplateMarketDataDummyMessage:   {},
}

// Additional template IDs (schema 9) not decoded in v1.0 but needed to
// walk FUT-INFO/OPT-INFO packets.
const (
	TemplateSecurityDefinitionUpdate uint16 = 10
	TemplateSecurityMassStatus       uint16 = 19
	TemplateSecurityGroupStatus      uint16 = 22
	TemplateDiscreteAuction          uint16 = 24
	TemplateTradingSessionStatus     uint16 = 26
	TemplateSecurityStatus           uint16 = 28
	TemplateLogon                    uint16 = 1000
	TemplateLogout                   uint16 = 1001
	TemplateMarketDataRequest        uint16 = 1002
	TemplateMarketDataDummyMessage   uint16 = 1003
)

// messageSize returns the number of body bytes (after the SBE header)
// occupied by a message of template hdr.TemplateID, or -1 if unknown.
func messageSize(hdr SBEHeader, body []byte) (int, error) {
	var shape templateShape
	var known bool
	shape, known = templateShapes[hdr.TemplateID]
	if !known || shape.unsizable {
		return -1, nil
	}
	var off int = int(hdr.BlockLength)
	if off > len(body) {
		return 0, fmt.Errorf("simba: template %d root block %d exceeds body %d", hdr.TemplateID, hdr.BlockLength, len(body))
	}
	for gi, dim := range shape.groupDims {
		if len(body) < off+dim {
			return 0, fmt.Errorf("simba: template %d group %d dimensions truncated", hdr.TemplateID, gi)
		}
		var blockLen int = int(binary.LittleEndian.Uint16(body[off : off+2]))
		var num int
		if dim == 3 {
			num = int(body[off+2])
		} else {
			num = int(binary.LittleEndian.Uint16(body[off+2 : off+4]))
		}
		off += dim + blockLen*num
		if off > len(body) {
			return 0, fmt.Errorf("simba: template %d group %d entries truncated", hdr.TemplateID, gi)
		}
	}
	for vi := 0; vi < shape.varData; vi++ {
		if len(body) < off+2 {
			return 0, fmt.Errorf("simba: template %d var-data %d length truncated", hdr.TemplateID, vi)
		}
		off += 2 + int(binary.LittleEndian.Uint16(body[off:off+2]))
		if off > len(body) {
			return 0, fmt.Errorf("simba: template %d var-data %d truncated", hdr.TemplateID, vi)
		}
	}
	return off, nil
}

// PacketInfo — the outer headers of one datagram.
type PacketInfo struct {
	Header      PacketHeader
	Incremental IncrementalHeader // valid iff Header.IsIncremental().
	// Messages — number of SBE messages delivered to fn.
	Messages int
	// Truncated — the last message could not be sized from the schema
	// (body shorter than its declared groups/var-data): it was delivered
	// as the remainder of the packet and per-message decoders apply their
	// own bounds checks. A harness should count these; a live feed should
	// treat them as corruption.
	Truncated bool
}

// Walk parses the packet headers of buf and calls fn for every SBE message
// in order with its header and body (root block + groups + var-data, sized
// per the schema; for an unsizable last message, body is the rest of the
// packet). fn returns false to stop early. The body slices alias buf.
func Walk(buf []byte, fn func(hdr SBEHeader, body []byte) bool) (PacketInfo, error) {
	var info PacketInfo
	var rest []byte
	var err error
	info.Header, rest, err = ParsePacketHeader(buf)
	if err != nil {
		return info, err
	}
	if info.Header.IsIncremental() {
		info.Incremental, rest, err = parseIncrementalHeader(rest)
		if err != nil {
			return info, err
		}
	}
	for len(rest) > 0 {
		var hdr SBEHeader
		hdr, rest, err = parseSBEHeader(rest)
		if err != nil {
			return info, err
		}
		var size int
		size, err = messageSize(hdr, rest)
		if err != nil {
			info.Truncated = true
			size = -1
		}
		var body []byte
		if size < 0 {
			// Unsizable (or truncated) message: it takes the rest of the
			// packet and must be the last one.
			body = rest
			rest = nil
		} else {
			body = rest[:size]
			rest = rest[size:]
		}
		info.Messages++
		if !fn(hdr, body) {
			return info, nil
		}
	}
	return info, nil
}

// PacketHeaderSeq returns MsgSeqNum of a datagram without full parsing
// (0 if the buffer is too short).
func PacketHeaderSeq(buf []byte) uint32 {
	if len(buf) < 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(buf[0:4])
}

// DecodeMessage decodes one SBE message (as delivered by Walk) into the
// typed payload fields of Decoded. Headers in out are left untouched.
func DecodeMessage(hdr SBEHeader, body []byte, out *Decoded) error {
	var err error
	switch hdr.TemplateID {
	case TemplateHeartbeat:
	case TemplateSequenceReset:
		if len(body) < 4 {
			return fmt.Errorf("simba: SequenceReset body too short")
		}
		var v uint32 = binary.LittleEndian.Uint32(body[0:4])
		out.NewSeqNo = &v
	case TemplateBestPrices:
		out.BestPrices, err = decodeBestPrices(body, hdr.BlockLength)
	case TemplateEmptyBook:
		out.EmptyBook, err = decodeEmptyBook(body)
	case TemplateOrderUpdate:
		out.OrderUpdate, err = decodeOrderUpdate(body)
	case TemplateOrderExecution:
		out.OrderExecution, err = decodeOrderExecution(body)
	case TemplateOrderBookSnapshot:
		out.OrderBookSnapshot, err = decodeOrderBookSnapshot(body, hdr.BlockLength)
	case TemplateSecurityDefinition:
		out.SecurityDefinition, err = decodeSecurityDefinitionPrefix(body)
	default:
		// Unknown/unsupported template — not an error, just not decoded.
	}
	return err
}
