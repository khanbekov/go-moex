package fix

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

// mockGate is a minimal FIX Gate stand-in: accepts one connection, replies
// to Logon with Logon, to Heartbeat/TestRequest with Heartbeat, and forwards
// application messages to appCh for assertions.
type mockGate struct {
	listener net.Listener
	appCh    chan *Message
}

func newMockGate(t *testing.T) *mockGate {
	t.Helper()
	var ln net.Listener
	var err error
	ln, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var g *mockGate = &mockGate{listener: ln, appCh: make(chan *Message, 16)}
	go g.serve(t)
	return g
}

func (g *mockGate) addr() string { return g.listener.Addr().String() }

func (g *mockGate) serve(t *testing.T) {
	conn, err := g.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	var fr *frameReader = newFrameReader(conn)
	var outSeq int64 = 1
	var send = func(m *Message) {
		var raw []byte = Encode("FD", "SENDER", outSeq, utcTimestamp(time.Now()), m)
		outSeq++
		_, _ = conn.Write(raw)
	}

	for {
		raw, err := fr.ReadMessage()
		if err != nil {
			return
		}
		msg, err := Decode(raw)
		if err != nil {
			continue
		}
		switch msg.MsgType() {
		case MsgTypeLogon:
			send(NewMessage(MsgTypeLogon).SetInt(TagEncryptMethod, 0).SetInt(TagHeartBtInt, 1))
		case MsgTypeTestRequest:
			testReqID, _ := msg.GetStr(TagTestReqID)
			send(NewMessage(MsgTypeHeartbeat).SetStr(TagTestReqID, testReqID))
		case MsgTypeHeartbeat:
			// no reply needed
		case MsgTypeLogout:
			send(NewMessage(MsgTypeLogout))
			return
		default:
			select {
			case g.appCh <- msg:
			default:
			}
		}
	}
}

func TestSessionLogonAndAppRoundTrip(t *testing.T) {
	var gate *mockGate = newMockGate(t)
	defer gate.listener.Close()

	host, portStr, err := net.SplitHostPort(gate.addr())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	var port int
	port, err = strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	var cfg Config = Config{
		Host:                    host,
		Port:                    port,
		SenderCompID:            "SENDER",
		TargetCompID:            "FD",
		HeartBtInt:              200 * time.Millisecond,
		ResetSeqNumFlag:         true,
		DialTimeout:             2 * time.Second,
		ReconnectInitialBackoff: 10 * time.Millisecond,
		ReconnectMaxBackoff:     20 * time.Millisecond,
	}

	var session *Session
	session, err = Dial(cfg)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	var received []*Message
	session.SetAppHandler(func(m *Message) { received = append(received, m) })

	var ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err = session.Logon(ctx); err != nil {
		t.Fatalf("Logon: %v", err)
	}

	var order *Message = NewMessage(MsgTypeNewOrderSingle)
	order.SetStr(TagClOrdID, "CL-1")
	order.SetStr(TagSymbol, "Si-12.25")

	var seq int64
	seq, err = session.SendApp(order)
	if err != nil {
		t.Fatalf("SendApp: %v", err)
	}
	if seq != 2 { // seq 1 was Logon.
		t.Fatalf("SendApp seq = %d, want 2", seq)
	}

	select {
	case got := <-gate.appCh:
		if got.MsgType() != MsgTypeNewOrderSingle {
			t.Fatalf("gate received MsgType = %q, want %q", got.MsgType(), MsgTypeNewOrderSingle)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the mock gate to receive NewOrderSingle")
	}

	if err = session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

