/*
FILE: internal/fix/fields.go

DESCRIPTION:
FIX tag constants used by the FORTS FIX Gate wire protocol (FIX 4.4 standard
header/trailer tags + FORTS-specific application tags, per
spectra_fixgate_en.pdf). Kept as plain ints (not a generated tag package
like quickfixgo's) — v1.0 only needs the ~30 tags actually used by New
Order Single / Order Cancel / Order Cancel-Replace / Order Mass Cancel /
Order Status Request / Execution Report / Order Cancel Reject / Order Mass
Cancel Report and the session layer, so hand-written constants are both
simpler and allocation-free to reference.
*/
package fix

// Standard header (§2.2) / trailer (§2.3).
const (
	TagBeginString     = 8
	TagBodyLength      = 9
	TagMsgType         = 35
	TagSenderCompID    = 49
	TagTargetCompID    = 56
	TagMsgSeqNum       = 34
	TagSendingTime     = 52
	TagOrigSendingTime = 122
	TagPossResend      = 97
	TagPossDupFlag     = 43
	TagCheckSum        = 10
)

// Session layer (§3).
const (
	TagEncryptMethod   = 98
	TagHeartBtInt      = 108
	TagResetSeqNumFlag = 141
	TagText            = 58
	TagTestReqID       = 112
	TagRefSeqNum       = 45
	TagRefTagID        = 371
	TagRefMsgType      = 372
	TagBeginSeqNo      = 7
	TagEndSeqNo        = 16
	TagGapFillFlag     = 123
	TagNewSeqNo        = 36
)

// Trading application layer (§4).
const (
	TagTransactTime        = 60
	TagClOrdID             = 11
	TagClOrdLinkID         = 583
	TagOrdType             = 40
	TagSymbol              = 55
	TagCFICode             = 461
	TagAccount             = 1
	TagTimeInForce         = 59
	TagSide                = 54
	TagOrderQty            = 38
	TagPrice               = 44
	TagOrderID             = 37
	TagOrigClOrdID         = 41
	TagExpireDate          = 432
	TagMarketSegmentID     = 1300
	TagFlags               = 20008
	TagNccRequest          = 20035
	TagComplianceID        = 376
	TagMassCancelReqType   = 530
	TagMassCancelResponse  = 531
	TagMassCancelRejReason = 532
	TagTotalAffectedOrders = 533
	TagOrdStatusReqID      = 790
	TagExecType            = 150
	TagOrdStatus           = 39
	TagExecID              = 17
	TagAccountLong         = 1 // Execution Report reuses tag 1 (up to 7 chars).
	TagLeavesQty           = 151
	TagCumQty              = 14
	TagAvgPx               = 6
	TagOrdRejReason        = 103
	TagSecondaryClOrdID    = 526
	TagLastPx              = 31
	TagLastQty             = 32
	TagCxlRejResponseTo    = 434
	TagCxlRejReason        = 102
)

// MsgType values (tag 35).
const (
	MsgTypeLogon               = "A"
	MsgTypeLogout              = "5"
	MsgTypeHeartbeat           = "0"
	MsgTypeTestRequest         = "1"
	MsgTypeResendRequest       = "2"
	MsgTypeReject              = "3"
	MsgTypeSequenceReset       = "4"
	MsgTypeNewOrderSingle      = "D"
	MsgTypeOrderCancelRequest  = "F"
	MsgTypeOrderCancelReplace  = "G"
	MsgTypeOrderStatusRequest  = "H"
	MsgTypeOrderMassCancelReq  = "q"
	MsgTypeExecutionReport     = "8"
	MsgTypeOrderCancelReject   = "9"
	MsgTypeOrderMassCancelRept = "r"
)
