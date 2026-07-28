/*
FILE: internal/fix/session.go

DESCRIPTION:
FIX 4.4 session-layer state machine for FORTS FIX Gate (§3 of
spectra_fixgate_en.pdf): Logon handshake, MsgSeqNum bookkeeping (separate
in/out counters, per spec §3.2), Heartbeat scheduling, TestRequest replies,
and the basic gap-recovery flow (ResendRequest on an incoming sequence gap).
Everything that is NOT session layer (New Order Single, Execution Report,
etc.) is handed to the caller-supplied AppHandler unmodified as a *Message —
forts/trading.go owns FORTS-specific interpretation, keeping this package
free of any FORTS trading semantics (it would work unchanged for a future
shares/ FIX Gate too).

CONCURRENCY MODEL:
  - One reader goroutine (readLoop) — the only writer of inSeq.
  - One heartbeat goroutine (heartbeatLoop) — timer-driven Heartbeat sends.
  - Writes to the socket are serialized by writeMu (SendApp and the
    session layer's own Heartbeat/TestRequest/Logout replies all go
    through send()).
  - Both goroutines exit when ctx is canceled or the connection errors,
    honoring the "no goroutine leaks on context cancellation" requirement.
*/
package fix

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tonymontanov/go-moex/internal/moexlog"
)

// AppHandler receives every non-session-layer message in arrival order.
// Called from the session's readLoop goroutine — must not block for long
// (queue/forward to a channel if the caller needs to do slow work).
type AppHandler func(*Message)

// Session — one FIX Gate TCP session.
type Session struct {
	cfg Config

	conn net.Conn
	fr   *frameReader

	writeMu sync.Mutex

	outSeq int64 // next outgoing MsgSeqNum, protected by writeMu.
	inSeq  int64 // next expected incoming MsgSeqNum, only touched by readLoop.

	appHandler AppHandler

	logonAckCh chan error
	closedCh   chan struct{}
	closeOnce  sync.Once

	lastErr atomic.Value // error
}

// Dial opens the TCP connection to FIX Gate. Does not perform the Logon
// handshake yet — call Logon after setting an AppHandler via SetAppHandler.
func Dial(cfg Config) (*Session, error) {
	var addr string = net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	var conn net.Conn
	var err error
	conn, err = net.DialTimeout("tcp", addr, cfg.DialTimeout)
	if err != nil {
		return nil, fmt.Errorf("fix: dial %s: %w", addr, err)
	}
	var s *Session = &Session{
		cfg:        cfg,
		conn:       conn,
		fr:         newFrameReader(conn),
		outSeq:     1,
		inSeq:      1,
		logonAckCh: make(chan error, 1),
		closedCh:   make(chan struct{}),
	}
	return s, nil
}

// SetAppHandler registers the application-message callback. Must be called
// before Logon.
func (s *Session) SetAppHandler(h AppHandler) { s.appHandler = h }

// SeedSeqNums sets the starting out/in sequence numbers (resumed session,
// same trading day — see spec §3.2.1 "FIX client must send 'Logon' message
// with MsgSeqNum = sequence number of the last message in out log + 1").
// Must be called before Logon; ignored if cfg.ResetSeqNumFlag is true.
func (s *Session) SeedSeqNums(outSeq, inSeq int64) {
	if s.cfg.ResetSeqNumFlag {
		return
	}
	s.outSeq = outSeq
	s.inSeq = inSeq
}

// Logon performs the Logon handshake and, on success, starts the readLoop
// and heartbeatLoop goroutines bound to ctx. Blocks until the counterparty's
// Logon reply arrives or ctx/timeout expires.
func (s *Session) Logon(ctx context.Context) error {
	var logon *Message = NewMessage(MsgTypeLogon)
	logon.SetInt(TagEncryptMethod, 0)
	logon.SetInt(TagHeartBtInt, int64(s.cfg.HeartBtInt/time.Second))
	if s.cfg.ResetSeqNumFlag {
		logon.SetStr(TagResetSeqNumFlag, "Y")
		s.outSeq = 1
		s.inSeq = 1
	}

	go s.readLoop(ctx)

	var err error = s.send(logon)
	if err != nil {
		return fmt.Errorf("fix: send Logon: %w", err)
	}

	select {
	case err = <-s.logonAckCh:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
		return fmt.Errorf("fix: Logon timed out waiting for reply")
	}

	go s.heartbeatLoop(ctx)
	return nil
}

// SendApp assigns the next outgoing MsgSeqNum, encodes msg and writes it to
// the socket. Returns the assigned sequence number.
func (s *Session) SendApp(msg *Message) (int64, error) {
	return s.sendSeq(msg)
}

func (s *Session) send(msg *Message) error {
	_, err := s.sendSeq(msg)
	return err
}

func (s *Session) sendSeq(msg *Message) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var seq int64 = s.outSeq
	var raw []byte = Encode(s.cfg.SenderCompID, s.cfg.TargetCompID, seq, utcTimestamp(time.Now()), msg)
	_, err := s.conn.Write(raw)
	if err != nil {
		s.fail(err)
		return 0, fmt.Errorf("fix: write %s: %w", msg.MsgType(), err)
	}
	s.outSeq = seq + 1
	if s.cfg.Metrics != nil {
		s.cfg.Metrics.Counter("moex_fix_messages_sent_total", "msg_type", msg.MsgType()).Inc()
	}
	return seq, nil
}

// Close sends Logout (best-effort) and closes the TCP connection. Safe to
// call multiple times.
func (s *Session) Close() error {
	var err error
	s.closeOnce.Do(func() {
		var logout *Message = NewMessage(MsgTypeLogout)
		_ = s.send(logout) // best-effort; ignore errors on the way out.
		close(s.closedCh)
		err = s.conn.Close()
	})
	return err
}

// Err returns the last fatal error observed by the session (nil if none).
func (s *Session) Err() error {
	v := s.lastErr.Load()
	if v == nil {
		return nil
	}
	return v.(error)
}

func (s *Session) fail(err error) {
	s.lastErr.Store(err)
	s.logger().Error("fix: session failed", moexlog.Err(err))
}

func (s *Session) logger() moexlog.Logger {
	if s.cfg.Logger != nil {
		return s.cfg.Logger
	}
	return moexlog.Noop()
}

// readLoop is the sole owner of s.inSeq. Dispatches session-layer messages
// internally and forwards everything else to s.appHandler.
func (s *Session) readLoop(ctx context.Context) {
	defer func() {
		select {
		case s.logonAckCh <- fmt.Errorf("fix: connection closed before Logon ack"):
		default:
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.closedCh:
			return
		default:
		}

		var raw []byte
		var err error
		raw, err = s.fr.ReadMessage()
		if err != nil {
			select {
			case <-s.closedCh:
				return // expected: Close() torn down the socket underneath us.
			default:
			}
			s.fail(fmt.Errorf("fix: read: %w", err))
			return
		}

		var msg *Message
		msg, err = Decode(raw)
		if err != nil {
			s.logger().Warn("fix: decode error, skipping frame", moexlog.Err(err))
			continue
		}
		if s.cfg.Metrics != nil {
			s.cfg.Metrics.Counter("moex_fix_messages_received_total", "msg_type", msg.MsgType()).Inc()
		}

		s.trackIncomingSeq(msg)
		s.dispatch(msg)
	}
}

// trackIncomingSeq implements the sequence-gap detection described in
// spec §3.2.2/§3.2.5. On a gap (incoming > expected) it requests a resend;
// on a lower-than-expected sequence without PossDupFlag it treats the
// session as unrecoverable (per spec recommendation) and fails it.
func (s *Session) trackIncomingSeq(msg *Message) {
	var seqStr, ok = msg.GetStr(TagMsgSeqNum)
	if !ok {
		return
	}
	var seq int64
	seq, _ = strconv.ParseInt(seqStr, 10, 64)
	if seq == 0 {
		return
	}

	switch {
	case seq == s.inSeq:
		s.inSeq++
	case seq > s.inSeq:
		s.logger().Warn("fix: sequence gap detected, requesting resend",
			moexlog.Int("expected", s.inSeq), moexlog.Int("got", seq))
		var rr *Message = NewMessage(MsgTypeResendRequest)
		rr.SetInt(TagBeginSeqNo, s.inSeq)
		rr.SetInt(TagEndSeqNo, seq-1)
		_ = s.send(rr)
		s.inSeq = seq + 1
	default: // seq < s.inSeq
		var possDup, _ = msg.GetStr(TagPossDupFlag)
		if possDup != "Y" {
			s.fail(fmt.Errorf("fix: incoming MsgSeqNum %d lower than expected %d without PossDupFlag — session unrecoverable, per spec must be terminated", seq, s.inSeq))
		}
		// PossDupFlag=Y resend of an already-processed message: ignore, do not advance inSeq.
	}
}

func (s *Session) dispatch(msg *Message) {
	switch msg.MsgType() {
	case MsgTypeLogon:
		select {
		case s.logonAckCh <- nil:
		default:
		}
	case MsgTypeHeartbeat:
		// Nothing to do — receipt alone confirms the session is alive.
	case MsgTypeTestRequest:
		var testReqID, _ = msg.GetStr(TagTestReqID)
		var hb *Message = NewMessage(MsgTypeHeartbeat)
		hb.SetStr(TagTestReqID, testReqID)
		_ = s.send(hb)
	case MsgTypeResendRequest:
		// v1.0: FORTS FIX Gate does not need us to resend application
		// messages (order entry is not something FIX Gate replays back
		// from a client) — acknowledge with SequenceReset/GapFill so the
		// server does not wait for a real resend.
		var beginStr, _ = msg.GetStr(TagBeginSeqNo)
		var beginSeq int64
		beginSeq, _ = strconv.ParseInt(beginStr, 10, 64)
		_ = beginSeq
		var sr *Message = NewMessage(MsgTypeSequenceReset)
		sr.SetStr(TagGapFillFlag, "Y")
		sr.SetInt(TagNewSeqNo, s.outSeq)
		_ = s.send(sr)
	case MsgTypeSequenceReset:
		var newSeqStr, _ = msg.GetStr(TagNewSeqNo)
		var newSeq int64
		newSeq, _ = strconv.ParseInt(newSeqStr, 10, 64)
		if newSeq > 0 {
			s.inSeq = newSeq
		}
	case MsgTypeLogout:
		s.logger().Info("fix: received Logout from counterparty")
		go func() { _ = s.Close() }()
	case MsgTypeReject:
		s.logger().Warn("fix: received session-level Reject", moexlog.Str("raw", fmt.Sprintf("%+v", msg)))
	default:
		if s.appHandler != nil {
			s.appHandler(msg)
		}
	}
}

func (s *Session) heartbeatLoop(ctx context.Context) {
	var interval time.Duration = s.cfg.HeartBtInt
	if interval <= 0 {
		interval = 30 * time.Second
	}
	var ticker *time.Ticker = time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.closedCh:
			return
		case <-ticker.C:
			var hb *Message = NewMessage(MsgTypeHeartbeat)
			if err := s.send(hb); err != nil {
				return
			}
		}
	}
}

// utcTimestamp formats t per FIX UTCTimestamp with millisecond precision:
// YYYYMMDD-HH:MM:SS.sss (spec §2.1 "Data types").
func utcTimestamp(t time.Time) string {
	return t.UTC().Format("20060102-15:04:05.000")
}

// jitteredBackoff computes a reconnect delay honoring cfg.Reconnect* and the
// spec-mandated 30s floor between sessions (§3.2.1: "the 'Logon' message
// must be resent no earlier than 30 seconds after the end of previous
// session"). Exported for use by the caller's reconnect loop (forts/client.go).
func JitteredBackoff(cfg Config, attempt int) time.Duration {
	var d time.Duration = cfg.ReconnectInitialBackoff << attempt
	if d > cfg.ReconnectMaxBackoff || d <= 0 {
		d = cfg.ReconnectMaxBackoff
	}
	if cfg.ReconnectJitter > 0 {
		var jitter float64 = 1 + (rand.Float64()*2-1)*cfg.ReconnectJitter
		d = time.Duration(float64(d) * jitter)
	}
	const minReconnectFloor = 30 * time.Second
	if d < minReconnectFloor {
		d = minReconnectFloor
	}
	return d
}
