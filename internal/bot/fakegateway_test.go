package bot

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// startBlockingGateway runs a fake Discord gateway that upgrades the websocket
// and then sends a non-JSON frame, the way an intercepting proxy serving
// Cloudflare's plain-text "error code: 1015" block page would. It returns the
// address to hand back from the gateway REST call, and a counter of accepted
// connections.
func startBlockingGateway(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	var hits atomic.Int64
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			hits.Add(1)
			go serveNonJSONFrame(conn)
		}
	}()
	return ln.Addr().String(), &hits
}

// serveNonJSONFrame completes a websocket upgrade and then writes a text frame
// whose body is not JSON.
func serveNonJSONFrame(conn net.Conn) {
	defer conn.Close()

	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	key := req.Header.Get("Sec-WebSocket-Key")
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	if _, err := conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n")); err != nil {
		return
	}

	payload := []byte("error code: 1015")
	frame := append([]byte{0x81, byte(len(payload))}, payload...)
	_, _ = conn.Write(frame)

	time.Sleep(200 * time.Millisecond)
}
