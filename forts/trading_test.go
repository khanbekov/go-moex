package forts

import (
	"testing"

	"github.com/tonymontanov/go-moex/forts/types"
	"github.com/tonymontanov/go-moex/internal/fix"
)

func newExecutionReport(t *testing.T) *fix.Message {
	t.Helper()
	var msg *fix.Message = fix.NewMessage(fix.MsgTypeExecutionReport)
	msg.SetInt(fix.TagOrderID, 123456)
	msg.SetStr(fix.TagClOrdID, "GM1")
	msg.SetStr(fix.TagSymbol, "Si-12.26")
	msg.SetStr(fix.TagSide, string(types.SideBuy))
	msg.SetStr(fix.TagPrice, "95000.5")
	msg.SetInt(fix.TagOrderQty, 10)
	msg.SetInt(fix.TagLeavesQty, 6)
	msg.SetInt(fix.TagCumQty, 4)
	msg.SetStr(fix.TagAvgPx, "95001")
	msg.SetStr(fix.TagOrdStatus, string(types.OrdStatusPartiallyFilled))
	msg.SetStr(fix.TagAccount, "ABC")
	msg.SetStr(fix.TagTransactTime, "20260728-12:00:00.000")
	return msg
}

func TestOrderInfoFromExecutionReport(t *testing.T) {
	var msg *fix.Message = newExecutionReport(t)
	var info *types.OrderInfo = orderInfoFromExecutionReport(msg)

	if info.OrderID != 123456 {
		t.Errorf("OrderID = %d, want 123456", info.OrderID)
	}
	if info.ClientOrderID != "GM1" {
		t.Errorf("ClientOrderID = %q, want GM1", info.ClientOrderID)
	}
	if info.Symbol != "Si-12.26" {
		t.Errorf("Symbol = %q, want Si-12.26", info.Symbol)
	}
	if info.Side != types.SideBuy {
		t.Errorf("Side = %q, want %q", info.Side, types.SideBuy)
	}
	if !info.Price.Equal(decStr(t, "95000.5")) {
		t.Errorf("Price = %s, want 95000.5", info.Price)
	}
	if info.Quantity != 10 || info.LeavesQty != 6 || info.CumQty != 4 {
		t.Errorf("Quantity/LeavesQty/CumQty = %d/%d/%d, want 10/6/4", info.Quantity, info.LeavesQty, info.CumQty)
	}
	if !info.AvgPx.Equal(decStr(t, "95001")) {
		t.Errorf("AvgPx = %s, want 95001", info.AvgPx)
	}
	if info.Status != types.OrdStatusPartiallyFilled {
		t.Errorf("Status = %q, want %q", info.Status, types.OrdStatusPartiallyFilled)
	}
	if info.Account != "ABC" {
		t.Errorf("Account = %q, want ABC", info.Account)
	}
	if info.TransactTimeMs == 0 {
		t.Error("TransactTimeMs should be non-zero when tag 60 is present")
	}
}

func TestOrderInfoFromExecutionReportMissingOptionalFields(t *testing.T) {
	var msg *fix.Message = fix.NewMessage(fix.MsgTypeExecutionReport)
	msg.SetInt(fix.TagOrderID, 1)
	msg.SetStr(fix.TagOrdStatus, string(types.OrdStatusRejected))
	msg.SetStr(fix.TagText, "insufficient margin")

	var info *types.OrderInfo = orderInfoFromExecutionReport(msg)
	if info.Status != types.OrdStatusRejected {
		t.Errorf("Status = %q, want %q", info.Status, types.OrdStatusRejected)
	}
	if info.RejectReasonText != "insufficient margin" {
		t.Errorf("RejectReasonText = %q, want %q", info.RejectReasonText, "insufficient margin")
	}
	if !info.Price.IsZero() {
		t.Errorf("Price = %s, want 0 when tag 44 is absent", info.Price)
	}
	if info.TransactTimeMs != 0 {
		t.Errorf("TransactTimeMs = %d, want 0 when tag 60 is absent", info.TransactTimeMs)
	}
}

func TestDecimalFromFIXStrInvalid(t *testing.T) {
	var msg *fix.Message = fix.NewMessage(fix.MsgTypeExecutionReport)
	msg.SetStr(fix.TagPrice, "not-a-number")
	if got := decimalFromFIXStr(msg, fix.TagPrice); !got.IsZero() {
		t.Errorf("decimalFromFIXStr with invalid input = %s, want 0", got)
	}
}

func TestTradeQtyAndPriceOf(t *testing.T) {
	var msg *fix.Message = fix.NewMessage(fix.MsgTypeExecutionReport)
	msg.SetInt(fix.TagLastQty, 3)
	msg.SetStr(fix.TagLastPx, "95500.25")

	if got := tradeQtyOf(msg); got != 3 {
		t.Errorf("tradeQtyOf = %d, want 3", got)
	}
	if got := tradePxOf(msg); !got.Equal(decStr(t, "95500.25")) {
		t.Errorf("tradePxOf = %s, want 95500.25", got)
	}
}
