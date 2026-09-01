package forts

import (
	"encoding/binary"
	"testing"

	"github.com/tonymontanov/go-moex/internal/simba"
	"github.com/tonymontanov/go-moex/orderbook"
)

// encodeOrderUpdatePacketForTest builds a raw SIMBA datagram carrying one
// OrderUpdate(15) message, mirroring the wire layout documented in
// internal/simba/decode.go (used here as a black-box round trip through
// simba.DecodePacket, exactly like a real UDP payload would be handled by
// handleSIMBAPacket).
func encodeOrderUpdatePacketForTest(t *testing.T, u simba.OrderUpdate) []byte {
	t.Helper()
	var buf []byte

	var packetHeader [16]byte
	binary.LittleEndian.PutUint32(packetHeader[0:4], 1)
	binary.LittleEndian.PutUint16(packetHeader[6:8], simba.FlagIncrementalPacket|simba.FlagLastFragment)
	buf = append(buf, packetHeader[:]...)

	var incHeader [12]byte
	buf = append(buf, incHeader[:]...)

	const orderUpdateSize = 50
	var sbeHeader [8]byte
	binary.LittleEndian.PutUint16(sbeHeader[0:2], orderUpdateSize)
	binary.LittleEndian.PutUint16(sbeHeader[2:4], simba.TemplateOrderUpdate)
	binary.LittleEndian.PutUint16(sbeHeader[4:6], 19780)
	binary.LittleEndian.PutUint16(sbeHeader[6:8], 9)
	buf = append(buf, sbeHeader[:]...)

	var body [orderUpdateSize]byte
	binary.LittleEndian.PutUint64(body[0:8], uint64(u.MDEntryID))
	binary.LittleEndian.PutUint64(body[8:16], uint64(u.MDEntryPx))
	binary.LittleEndian.PutUint64(body[16:24], uint64(u.MDEntrySize))
	binary.LittleEndian.PutUint64(body[24:32], u.MDFlags)
	binary.LittleEndian.PutUint64(body[32:40], u.MDFlags2)
	binary.LittleEndian.PutUint32(body[40:44], uint32(u.SecurityID))
	binary.LittleEndian.PutUint32(body[44:48], u.RptSeq)
	body[48] = byte(u.MDUpdateAction)
	body[49] = byte(u.MDEntryType)
	buf = append(buf, body[:]...)
	binary.LittleEndian.PutUint16(buf[4:6], uint16(len(buf))) // MsgSize — ParsePacket verifies it.

	return buf
}

func TestSideFromMDEntryType(t *testing.T) {
	if got := sideFromMDEntryType(simba.MDEntryTypeBid); got != orderbook.SideBid {
		t.Errorf("sideFromMDEntryType(Bid) = %v, want SideBid", got)
	}
	if got := sideFromMDEntryType(simba.MDEntryTypeOffer); got != orderbook.SideAsk {
		t.Errorf("sideFromMDEntryType(Offer) = %v, want SideAsk", got)
	}
}

func TestApplyOrderUpdateDeltaUpsertAndDelete(t *testing.T) {
	var engine *orderbook.Engine = orderbook.NewEngine()

	applyOrderUpdateDelta(engine, &simba.OrderUpdate{
		MDEntryID:      1,
		MDEntryPx:      9500000, // 95.0 at 1e-5.
		MDEntrySize:    10,
		SecurityID:     100,
		RptSeq:         1,
		MDUpdateAction: simba.MDUpdateActionNew,
		MDEntryType:    simba.MDEntryTypeBid,
	})

	var price, size, ok = engine.BestBid()
	if !ok || price != 9500000 || size != 10 {
		t.Fatalf("BestBid after upsert = (%d, %d, %v), want (9500000, 10, true)", price, size, ok)
	}

	applyOrderUpdateDelta(engine, &simba.OrderUpdate{
		MDEntryID:      1,
		SecurityID:     100,
		RptSeq:         2,
		MDUpdateAction: simba.MDUpdateActionDelete,
		MDEntryType:    simba.MDEntryTypeBid,
	})

	if _, _, ok = engine.BestBid(); ok {
		t.Error("BestBid should be empty after deleting the only resting order")
	}
}

func TestApplyOrderUpdateDeltaGapClearsEngine(t *testing.T) {
	var engine *orderbook.Engine = orderbook.NewEngine()
	applyOrderUpdateDelta(engine, &simba.OrderUpdate{MDEntryID: 1, MDEntryPx: 100, MDEntrySize: 1, RptSeq: 1, MDEntryType: simba.MDEntryTypeBid})
	applyOrderUpdateDelta(engine, &simba.OrderUpdate{MDEntryID: 2, MDEntryPx: 100, MDEntrySize: 1, RptSeq: 5, MDEntryType: simba.MDEntryTypeBid}) // gap: expected seq 2.

	if engine.OrderCount() != 0 {
		t.Errorf("OrderCount after a sequence gap = %d, want 0 (engine should be cleared)", engine.OrderCount())
	}
}

func TestApplyOrderExecutionDeltaSkipsNullPrice(t *testing.T) {
	var engine *orderbook.Engine = orderbook.NewEngine()
	applyOrderExecutionDelta(engine, &simba.OrderExecution{
		MDEntryID:   1,
		MDEntryPx:   simba.NullDecimalMantissa,
		RptSeq:      1,
		MDEntryType: simba.MDEntryTypeBid,
	})
	if engine.OrderCount() != 0 {
		t.Errorf("a technical trade leg (null price) should not touch the book, OrderCount = %d", engine.OrderCount())
	}
}

func TestApplyOrderExecutionDeltaUpsert(t *testing.T) {
	var engine *orderbook.Engine = orderbook.NewEngine()
	applyOrderExecutionDelta(engine, &simba.OrderExecution{
		MDEntryID:      1,
		MDEntryPx:      100,
		MDEntrySize:    5,
		RptSeq:         1,
		MDUpdateAction: simba.MDUpdateActionChange,
		MDEntryType:    simba.MDEntryTypeOffer,
	})
	var price, size, ok = engine.BestAsk()
	if !ok || price != 100 || size != 5 {
		t.Fatalf("BestAsk after OrderExecution upsert = (%d, %d, %v), want (100, 5, true)", price, size, ok)
	}
}

func TestLoadOrderBookSnapshotSkipsEmptyBookAndNullEntries(t *testing.T) {
	var engine *orderbook.Engine = orderbook.NewEngine()
	loadOrderBookSnapshot(engine, &simba.OrderBookSnapshot{
		SecurityID: 100,
		RptSeq:     42,
		Entries: []simba.OrderBookSnapshotEntry{
			{MDEntryID: 1, MDEntryPx: 100, MDEntrySize: 5, MDEntryType: simba.MDEntryTypeBid},
			{MDEntryID: 2, MDEntryPx: 200, MDEntrySize: 3, MDEntryType: simba.MDEntryTypeOffer},
			{MDEntryID: simba.NullInt64, MDEntryType: simba.MDEntryTypeBid},
			{MDEntryID: 3, MDEntryType: simba.MDEntryTypeEmptyBook},
		},
	})

	if engine.OrderCount() != 2 {
		t.Errorf("OrderCount = %d, want 2 (null/EmptyBook entries skipped)", engine.OrderCount())
	}
	var seq, ok = engine.LastSeq()
	if !ok || seq != 42 {
		t.Errorf("LastSeq = (%d, %v), want (42, true)", seq, ok)
	}

	var bidPrice, _, bidOk = engine.BestBid()
	if !bidOk || bidPrice != 100 {
		t.Errorf("BestBid = (%d, %v), want (100, true)", bidPrice, bidOk)
	}
	var askPrice, _, askOk = engine.BestAsk()
	if !askOk || askPrice != 200 {
		t.Errorf("BestAsk = (%d, %v), want (200, true)", askPrice, askOk)
	}
}

func TestHandleSIMBAPacketRoutesBySecurityID(t *testing.T) {
	var engine *orderbook.Engine = orderbook.NewEngine()
	var packet []byte = encodeOrderUpdatePacketForTest(t, simba.OrderUpdate{
		MDEntryID:      1,
		MDEntryPx:      100,
		MDEntrySize:    5,
		SecurityID:     999,
		RptSeq:         1,
		MDUpdateAction: simba.MDUpdateActionNew,
		MDEntryType:    simba.MDEntryTypeBid,
	})

	var changed bool
	handleSIMBAPacket(packet, 111, engine, func() { changed = true }) // Wrong SecurityID: no-op.
	if changed || engine.OrderCount() != 0 {
		t.Fatal("handleSIMBAPacket should ignore packets for a different SecurityID")
	}

	handleSIMBAPacket(packet, 999, engine, func() { changed = true })
	if !changed || engine.OrderCount() != 1 {
		t.Fatal("handleSIMBAPacket should apply packets matching the requested SecurityID")
	}
}

func TestLevelsToDecimal(t *testing.T) {
	var engine *orderbook.Engine = orderbook.NewEngine()
	applyOrderUpdateDelta(engine, &simba.OrderUpdate{MDEntryID: 1, MDEntryPx: 9500000, MDEntrySize: 10, RptSeq: 1, MDEntryType: simba.MDEntryTypeBid})

	var levels []orderbook.Level = engine.Levels(orderbook.SideBid, 0)
	var out = levelsToDecimal(levels)
	if len(out) != 1 {
		t.Fatalf("levelsToDecimal returned %d levels, want 1", len(out))
	}
	if !out[0].Price.Equal(decStr(t, "95")) {
		t.Errorf("Price = %s, want 95", out[0].Price)
	}
	if !out[0].Size.Equal(decStr(t, "10")) {
		t.Errorf("Size = %s, want 10", out[0].Size)
	}
}
