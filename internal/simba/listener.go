/*
FILE: internal/simba/listener.go

DESCRIPTION:
UDP multicast listener for one SIMBA SPECTRA stream (one of: Incremental A/B,
Snapshot A/B). MOEX broadcasts every stream on two independent multicast
groups (A/B) carrying the SAME data for redundancy — per spec §1.4.2, a
correct client joins BOTH and deduplicates by MsgSeqNum, tolerating loss on
either. Deduplication is the caller's responsibility (see orderbook
package); this type only delivers raw datagrams as they arrive.

REQUIRES EXCHANGE COLOCATION: multicast groups are only reachable from
MOEX's own network segment (colocation) or a broker-provided low-latency
circuit — see docs/handoff.md "Open questions". Outside of that
environment Listen will simply time out / receive nothing; that is
expected, not a bug.
*/
package simba

import (
	"context"
	"fmt"
	"net"

	"github.com/tonymontanov/go-moex/internal/moexlog"
)

// ListenerConfig — one multicast group subscription.
type ListenerConfig struct {
	// GroupAddr — multicast group address:port, e.g. "239.195.1.1:15000".
	GroupAddr string
	// Interface — local NIC name to join the group on (e.g. "eth0"). Empty
	// lets the OS pick the default multicast-capable interface — usually
	// wrong in a multi-NIC colocation host, so it should normally be set.
	Interface string
	// ReadBufferSize — per-read buffer size. SIMBA datagrams are small
	// (order of a few hundred bytes for OrderUpdate/OrderExecution, larger
	// for OrderBookSnapshot fragments) — 64KiB safely covers the UDP max.
	ReadBufferSize int
	// SocketReadBufferBytes — OS-level SO_RCVBUF size. 0 = OS default.
	// Bursty order-log traffic during volatile opens can overflow small
	// default kernel buffers; production colocation deployments should set
	// this explicitly (see docs/handoff.md "Open questions" — MOEX-
	// recommended value not yet confirmed).
	SocketReadBufferBytes int

	Logger moexlog.Logger
}

// Listener — one joined multicast group.
type Listener struct {
	cfg  ListenerConfig
	conn *net.UDPConn
}

// Listen resolves cfg.GroupAddr and joins the multicast group on
// cfg.Interface.
func Listen(cfg ListenerConfig) (*Listener, error) {
	var groupAddr *net.UDPAddr
	var err error
	groupAddr, err = net.ResolveUDPAddr("udp4", cfg.GroupAddr)
	if err != nil {
		return nil, fmt.Errorf("simba: resolve group addr %q: %w", cfg.GroupAddr, err)
	}

	var iface *net.Interface
	if cfg.Interface != "" {
		iface, err = net.InterfaceByName(cfg.Interface)
		if err != nil {
			return nil, fmt.Errorf("simba: interface %q: %w", cfg.Interface, err)
		}
	}

	var conn *net.UDPConn
	conn, err = net.ListenMulticastUDP("udp4", iface, groupAddr)
	if err != nil {
		return nil, fmt.Errorf("simba: join multicast group %q: %w", cfg.GroupAddr, err)
	}

	if cfg.ReadBufferSize <= 0 {
		cfg.ReadBufferSize = 64 * 1024
	}
	if cfg.SocketReadBufferBytes > 0 {
		_ = conn.SetReadBuffer(cfg.SocketReadBufferBytes)
	}

	return &Listener{cfg: cfg, conn: conn}, nil
}

// Close leaves the multicast group and closes the socket.
func (l *Listener) Close() error { return l.conn.Close() }

// Run reads datagrams until ctx is canceled or a read error occurs, calling
// handler synchronously for each one. handler must copy buf if it retains a
// reference beyond the call (the slice is reused across iterations).
//
// Speed contract: handler runs on the read goroutine — keep it O(1)
// (decode + forward to a channel/orderbook engine), exactly like the
// go-okx WS message handler contract.
func (l *Listener) Run(ctx context.Context, handler func(buf []byte)) error {
	var buf []byte = make([]byte, l.cfg.ReadBufferSize)

	go func() {
		<-ctx.Done()
		_ = l.conn.Close()
	}()

	for {
		var n int
		var err error
		n, _, err = l.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return fmt.Errorf("simba: read from %s: %w", l.cfg.GroupAddr, err)
			}
		}
		handler(buf[:n])
	}
}

func (l *Listener) logger() moexlog.Logger {
	if l.cfg.Logger != nil {
		return l.cfg.Logger
	}
	return moexlog.Noop()
}
