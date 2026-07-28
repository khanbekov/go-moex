/*
FILE: internal/simba/types.go

DESCRIPTION:
Wire-level types for SIMBA SPECTRA (FORTS market data), decoded per the
official SBE schema simba_spectra-9.0.xml
(https://ftp.moex.com/pub/SIMBA/Spectra/prod/simba_spectra-9.0.xml, schema
id=19780, version=9). All integers are little-endian (schema byteOrder
attribute). Byte layouts below are derived directly from the <composite>/
<message> definitions — see each decode function's doc for the exact
field-by-field offset table, so a future schema bump can be diffed
mechanically against the XML rather than against prose in a PDF.

v1.0 scope: only the messages needed to build and maintain a local FORTS
order book are decoded — Heartbeat, SequenceReset, BestPrices, EmptyBook,
OrderUpdate, OrderExecution, OrderBookSnapshot. SecurityDefinition/
SecurityStatus/TradingSessionStatus etc. are NOT decoded in v1.0: reference
data already comes from ISS (internal/iss), and trading-status gating is a
v1.x nice-to-have, not required to keep an order book correct — see
docs/handoff.md "Open questions" for the full deferred list.
*/
package simba

// MsgFlags bit constants (composite MsgFlagsSet, encodingType=uint16, in
// the Market Data Packet Header — NOT to be confused with MDFlagsSet
// carried inside OrderUpdate/OrderExecution).
const (
	FlagLastFragment      uint16 = 1 << 0
	FlagStartOfSnapshot   uint16 = 1 << 1
	FlagEndOfSnapshot     uint16 = 1 << 2
	FlagIncrementalPacket uint16 = 1 << 3
	FlagPossDupFlag       uint16 = 1 << 4
)

// SBE default null-value sentinels (Simple Binary Encoding spec, used
// whenever a *NULL type omits an explicit nullValue attribute in the
// schema — e.g. Int64NULL, uInt32NULL).
const (
	NullInt64  int64  = -9223372036854775808
	NullUint32 uint32 = 4294967295
	// NullDecimalMantissa — explicit nullValue for Decimal5NULL/Decimal2NULL
	// mantissa (schema: nullValue="9223372036854775807").
	NullDecimalMantissa int64 = 9223372036854775807
)

// MDUpdateAction — enum MDUpdateAction (encodingType=uInt8).
type MDUpdateAction uint8

const (
	MDUpdateActionNew    MDUpdateAction = 0
	MDUpdateActionChange MDUpdateAction = 1
	MDUpdateActionDelete MDUpdateAction = 2
)

// MDEntryType — enum MDEntryType (encodingType=Char).
type MDEntryType byte

const (
	MDEntryTypeBid       MDEntryType = '0'
	MDEntryTypeOffer     MDEntryType = '1'
	MDEntryTypeEmptyBook MDEntryType = 'J'
)

// PacketHeader — composite MarketDataPacketHeader (16 bytes wire size:
// uint32 + uint16 + uint16 + uint64).
type PacketHeader struct {
	MsgSeqNum   uint32
	MsgSize     uint16
	MsgFlags    uint16
	SendingTime uint64 // ns since Unix epoch, UTC.
}

func (h PacketHeader) IsIncremental() bool     { return h.MsgFlags&FlagIncrementalPacket != 0 }
func (h PacketHeader) IsLastFragment() bool    { return h.MsgFlags&FlagLastFragment != 0 }
func (h PacketHeader) IsStartOfSnapshot() bool { return h.MsgFlags&FlagStartOfSnapshot != 0 }
func (h PacketHeader) IsEndOfSnapshot() bool   { return h.MsgFlags&FlagEndOfSnapshot != 0 }
func (h PacketHeader) IsPossDup() bool         { return h.MsgFlags&FlagPossDupFlag != 0 }

// IncrementalHeader — composite IncrementalPacketHeader (12 bytes: uint64 +
// uint32), present only when PacketHeader.IsIncremental().
type IncrementalHeader struct {
	TransactTime             uint64
	ExchangeTradingSessionID uint32 // NullUint32 if absent.
}

// SBEHeader — composite messageHeader (8 bytes: 4 x uint16).
type SBEHeader struct {
	BlockLength uint16
	TemplateID  uint16
	SchemaID    uint16
	Version     uint16
}

// Message template IDs (sbe:message id=...).
const (
	TemplateHeartbeat          uint16 = 1
	TemplateSequenceReset      uint16 = 2
	TemplateEmptyBook          uint16 = 4
	TemplateBestPrices         uint16 = 14
	TemplateOrderUpdate        uint16 = 15
	TemplateOrderExecution     uint16 = 16
	TemplateOrderBookSnapshot  uint16 = 17
	TemplateSecurityDefinition uint16 = 27
)

// SecurityDefinition — PARTIAL decode of SecurityDefinition(27): only the
// Symbol<->SecurityID mapping fields, which happen to be the first three
// fields of the root block (TotNumReports uint32 + Symbol String25 +
// SecurityID Int32, byte offsets 0/4/29 — see decode.go). The message has
// many more fields (margins, price limits, CFICode, event dates, ...)
// that v1.0 does not need and therefore does not decode — see
// decode.go doc for why reading a fixed-offset prefix of an SBE root
// block is safe to do without decoding the rest.
type SecurityDefinition struct {
	Symbol     string
	SecurityID int32
}

// BestPricesEntry — one entry of the BestPrices(14) repeating group
// (36 bytes: Decimal5NULL x2 + Int64NULL x2 + Int32).
type BestPricesEntry struct {
	MktBidPx     int64 // mantissa, actual price = MktBidPx * 1e-5. NullDecimalMantissa if absent.
	MktOfferPx   int64
	MktBidSize   int64 // NullInt64 if absent.
	MktOfferSize int64
	SecurityID   int32
}

// BestPrices — decoded BestPrices(14) message.
type BestPrices struct {
	Entries []BestPricesEntry
}

// EmptyBook — decoded EmptyBook(4) message (4-byte root block).
type EmptyBook struct {
	LastMsgSeqNumProcessed uint32 // NullUint32 if absent.
}

// OrderUpdate — decoded OrderUpdate(15) message (50-byte flat root block,
// no repeating group).
type OrderUpdate struct {
	MDEntryID      int64
	MDEntryPx      int64 // mantissa, Decimal5 (always present, not nullable).
	MDEntrySize    int64
	MDFlags        uint64
	MDFlags2       uint64
	SecurityID     int32
	RptSeq         uint32
	MDUpdateAction MDUpdateAction
	MDEntryType    MDEntryType
}

// OrderExecution — decoded OrderExecution(16) message (74-byte flat root
// block, no repeating group).
type OrderExecution struct {
	MDEntryID      int64
	MDEntryPx      int64 // Decimal5NULL — NullDecimalMantissa if absent (technical trade legs).
	MDEntrySize    int64 // Int64NULL — NullInt64 if absent.
	LastPx         int64 // Decimal5, always present.
	LastQty        int64
	TradeID        int64
	MDFlags        uint64
	MDFlags2       uint64
	SecurityID     int32
	RptSeq         uint32
	MDUpdateAction MDUpdateAction
	MDEntryType    MDEntryType
}

// OrderBookSnapshotEntry — one entry of the OrderBookSnapshot(17) repeating
// group (57 bytes: Int64NULL + uint64 + Decimal5NULL + Int64NULL +
// Int64NULL + uint64 + uint64 + char).
type OrderBookSnapshotEntry struct {
	MDEntryID    int64 // NullInt64 if absent.
	TransactTime uint64
	MDEntryPx    int64 // NullDecimalMantissa if absent.
	MDEntrySize  int64 // NullInt64 if absent.
	TradeID      int64 // NullInt64 if absent.
	MDFlags      uint64
	MDFlags2     uint64
	MDEntryType  MDEntryType
}

// OrderBookSnapshot — decoded OrderBookSnapshot(17) message (16-byte root
// block + repeating group of OrderBookSnapshotEntry).
type OrderBookSnapshot struct {
	SecurityID               int32
	LastMsgSeqNumProcessed   uint32
	RptSeq                   uint32
	ExchangeTradingSessionID uint32
	Entries                  []OrderBookSnapshotEntry
}
