package simba

import (
	"encoding/binary"
	"testing"
)

// buildPacket assembles a MarketDataPacketHeader (+ optional
// IncrementalPacketHeader) + SBE messageHeader + body, mirroring exactly
// what a real SIMBA gateway would put on the wire.
func buildPacket(flags uint16, incremental bool, templateID uint16, blockLength uint16, body []byte) []byte {
	var buf []byte

	var packetHeader [16]byte
	binary.LittleEndian.PutUint32(packetHeader[0:4], 1)
	binary.LittleEndian.PutUint16(packetHeader[4:6], 0) // MsgSize, unused by decoder.
	binary.LittleEndian.PutUint16(packetHeader[6:8], flags)
	binary.LittleEndian.PutUint64(packetHeader[8:16], 1700000000000000000)
	buf = append(buf, packetHeader[:]...)

	if incremental {
		var incHeader [12]byte
		binary.LittleEndian.PutUint64(incHeader[0:8], 1700000000000000000)
		binary.LittleEndian.PutUint32(incHeader[8:12], 1)
		buf = append(buf, incHeader[:]...)
	}

	var sbeHeader [8]byte
	binary.LittleEndian.PutUint16(sbeHeader[0:2], blockLength)
	binary.LittleEndian.PutUint16(sbeHeader[2:4], templateID)
	binary.LittleEndian.PutUint16(sbeHeader[4:6], 19780)
	binary.LittleEndian.PutUint16(sbeHeader[6:8], 9)
	buf = append(buf, sbeHeader[:]...)

	buf = append(buf, body...)
	return buf
}

func TestDecodeOrderUpdate(t *testing.T) {
	var body [orderUpdateSize]byte
	binary.LittleEndian.PutUint64(body[0:8], 123456789)               // MDEntryID
	binary.LittleEndian.PutUint64(body[8:16], uint64(int64(9550000))) // MDEntryPx = 95.50000
	binary.LittleEndian.PutUint64(body[16:24], 10)                    // MDEntrySize
	binary.LittleEndian.PutUint64(body[24:32], 1)                     // MDFlags = Day
	binary.LittleEndian.PutUint64(body[32:40], 0)                     // MDFlags2
	binary.LittleEndian.PutUint32(body[40:44], 42)                    // SecurityID
	binary.LittleEndian.PutUint32(body[44:48], 7)                     // RptSeq
	body[48] = byte(MDUpdateActionNew)
	body[49] = byte(MDEntryTypeBid)

	var packet []byte = buildPacket(FlagIncrementalPacket|FlagLastFragment, true, TemplateOrderUpdate, orderUpdateSize, body[:])

	var decoded Decoded
	var err error
	decoded, err = DecodePacket(packet)
	if err != nil {
		t.Fatalf("DecodePacket: %v", err)
	}
	if !decoded.PacketHeader.IsIncremental() {
		t.Fatal("expected IsIncremental() == true")
	}
	if decoded.Incremental == nil {
		t.Fatal("expected non-nil IncrementalHeader")
	}
	if decoded.OrderUpdate == nil {
		t.Fatal("expected non-nil OrderUpdate")
	}
	var ou *OrderUpdate = decoded.OrderUpdate
	if ou.MDEntryID != 123456789 {
		t.Errorf("MDEntryID = %d, want 123456789", ou.MDEntryID)
	}
	if ou.MDEntryPx != 9550000 {
		t.Errorf("MDEntryPx = %d, want 9550000", ou.MDEntryPx)
	}
	if ou.SecurityID != 42 {
		t.Errorf("SecurityID = %d, want 42", ou.SecurityID)
	}
	if ou.MDUpdateAction != MDUpdateActionNew {
		t.Errorf("MDUpdateAction = %v, want New", ou.MDUpdateAction)
	}
	if ou.MDEntryType != MDEntryTypeBid {
		t.Errorf("MDEntryType = %v, want Bid", ou.MDEntryType)
	}
}

func TestDecodeBestPricesWithTwoEntries(t *testing.T) {
	const entrySize = bestPricesEntrySize
	var body []byte

	var groupHeader [3]byte
	binary.LittleEndian.PutUint16(groupHeader[0:2], entrySize)
	groupHeader[2] = 2
	body = append(body, groupHeader[:]...)

	for i := 0; i < 2; i++ {
		var entry [entrySize]byte
		binary.LittleEndian.PutUint64(entry[0:8], uint64(int64(1000000+i)))
		binary.LittleEndian.PutUint64(entry[8:16], uint64(int64(1000100+i)))
		binary.LittleEndian.PutUint64(entry[16:24], 5)
		binary.LittleEndian.PutUint64(entry[24:32], 7)
		binary.LittleEndian.PutUint32(entry[32:36], uint32(100+i))
		body = append(body, entry[:]...)
	}

	var packet []byte = buildPacket(0, false, TemplateBestPrices, 0, body)

	var decoded Decoded
	var err error
	decoded, err = DecodePacket(packet)
	if err != nil {
		t.Fatalf("DecodePacket: %v", err)
	}
	if decoded.Incremental != nil {
		t.Fatal("expected nil IncrementalHeader for a non-incremental packet")
	}
	if decoded.BestPrices == nil || len(decoded.BestPrices.Entries) != 2 {
		t.Fatalf("BestPrices = %+v, want 2 entries", decoded.BestPrices)
	}
	if decoded.BestPrices.Entries[1].SecurityID != 101 {
		t.Errorf("Entries[1].SecurityID = %d, want 101", decoded.BestPrices.Entries[1].SecurityID)
	}
}

func TestDecodeSecurityDefinitionPrefix(t *testing.T) {
	var body [4 + 25 + 4]byte
	binary.LittleEndian.PutUint32(body[0:4], 1) // TotNumReports
	copy(body[4:29], []byte("Si-12.25"))        // Symbol, padded with zero bytes.
	binary.LittleEndian.PutUint32(body[29:33], 987654)

	var packet []byte = buildPacket(0, false, TemplateSecurityDefinition, uint16(len(body)), body[:])

	var decoded Decoded
	var err error
	decoded, err = DecodePacket(packet)
	if err != nil {
		t.Fatalf("DecodePacket: %v", err)
	}
	if decoded.SecurityDefinition == nil {
		t.Fatal("expected non-nil SecurityDefinition")
	}
	if decoded.SecurityDefinition.Symbol != "Si-12.25" {
		t.Errorf("Symbol = %q, want %q", decoded.SecurityDefinition.Symbol, "Si-12.25")
	}
	if decoded.SecurityDefinition.SecurityID != 987654 {
		t.Errorf("SecurityID = %d, want 987654", decoded.SecurityDefinition.SecurityID)
	}
}

func TestDecodeEmptyBook(t *testing.T) {
	var body [4]byte
	binary.LittleEndian.PutUint32(body[0:4], NullUint32)
	var packet []byte = buildPacket(0, false, TemplateEmptyBook, 4, body[:])

	var decoded Decoded
	var err error
	decoded, err = DecodePacket(packet)
	if err != nil {
		t.Fatalf("DecodePacket: %v", err)
	}
	if decoded.EmptyBook == nil {
		t.Fatal("expected non-nil EmptyBook")
	}
	if decoded.EmptyBook.LastMsgSeqNumProcessed != NullUint32 {
		t.Errorf("LastMsgSeqNumProcessed = %d, want NullUint32", decoded.EmptyBook.LastMsgSeqNumProcessed)
	}
}
