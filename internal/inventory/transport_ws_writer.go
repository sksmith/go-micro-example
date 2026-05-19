package inventory

import (
	"net"

	"github.com/gobwas/ws/wsutil"
)

// textWriter is the surface the Subscribe streaming helpers
// (streamInventoryToClient, streamReservationsToClient) call into to
// emit a single WS text frame. Extracted as an interface so unit
// tests can drive the streaming logic against an in-memory recorder
// without an in-process WS dialer — the OPS-009 root cause was that
// the prior tests went through ws.Dialer + httptest.NewServer and
// flaked under the Go 1.24 scheduler on Linux/macOS runners.
type textWriter interface {
	WriteText(b []byte) error
}

// wsTextWriter is the production implementation: it forwards each
// frame through gobwas/ws/wsutil onto a real net.Conn.
type wsTextWriter struct{ conn net.Conn }

func (w wsTextWriter) WriteText(b []byte) error {
	return wsutil.WriteServerText(w.conn, b)
}
