package fix

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	var msg *Message = NewMessage(MsgTypeNewOrderSingle)
	msg.SetStr(TagClOrdID, "CL1")
	msg.SetStr(TagOrdType, "2")
	msg.SetStr(TagSymbol, "Si-12.25")
	msg.SetStr(TagAccount, "ABC")
	msg.SetStr(TagSide, "1")
	msg.SetInt(TagOrderQty, 10)
	msg.SetPrice(TagPrice, "95.50000")

	var raw []byte = Encode("SENDER", "FD", 42, "20251225-10:00:00.000", msg)

	var decoded *Message
	var err error
	decoded, err = Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if decoded.MsgType() != MsgTypeNewOrderSingle {
		t.Fatalf("MsgType = %q, want %q", decoded.MsgType(), MsgTypeNewOrderSingle)
	}
	if v, _ := decoded.GetStr(TagClOrdID); v != "CL1" {
		t.Fatalf("ClOrdID = %q, want CL1", v)
	}
	if v, _ := decoded.GetStr(TagSymbol); v != "Si-12.25" {
		t.Fatalf("Symbol = %q, want Si-12.25", v)
	}
	if v, _ := decoded.GetInt(TagOrderQty); v != 10 {
		t.Fatalf("OrderQty = %d, want 10", v)
	}
	if v, _ := decoded.GetStr(TagSenderCompID); v != "SENDER" {
		t.Fatalf("SenderCompID = %q, want SENDER", v)
	}
	if v, _ := decoded.GetInt(TagMsgSeqNum); v != 42 {
		t.Fatalf("MsgSeqNum = %d, want 42", v)
	}

	if err = VerifyChecksum(raw, decoded); err != nil {
		t.Fatalf("VerifyChecksum: %v", err)
	}
}

func TestEncodeBodyLengthIsCorrect(t *testing.T) {
	var msg *Message = NewMessage(MsgTypeHeartbeat)
	var raw []byte = Encode("SENDER", "FD", 1, "20251225-10:00:00.000", msg)

	var fr *frameReader = newFrameReader(bytes.NewReader(raw))
	var framed []byte
	var err error
	framed, err = fr.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if len(framed) != len(raw) {
		t.Fatalf("framed len = %d, want %d (BodyLength must exactly cover the message)", len(framed), len(raw))
	}
}

