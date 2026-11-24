package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func withRelayH2HealthSettings(t *testing.T) {
	t.Helper()
	prevMaxIdle := common.RelayMaxIdleConns
	prevPerHost := common.RelayMaxIdleConnsPerHost
	prevTimeout := common.RelayIdleConnTimeout
	prevPingIdle, prevPingTimeout := common.RelayHTTP2PingIdleTimeout, common.RelayHTTP2PingTimeout
	common.RelayMaxIdleConns = 500
	common.RelayMaxIdleConnsPerHost = 100
	common.RelayIdleConnTimeout = 90
	common.RelayHTTP2PingIdleTimeout, common.RelayHTTP2PingTimeout = 1, 1
	t.Cleanup(func() {
		common.RelayMaxIdleConns = prevMaxIdle
		common.RelayMaxIdleConnsPerHost = prevPerHost
		common.RelayIdleConnTimeout = prevTimeout
		common.RelayHTTP2PingIdleTimeout, common.RelayHTTP2PingTimeout = prevPingIdle, prevPingTimeout
	})
}

func testH2TLSClientConfig(t *testing.T, server *httptest.Server) *tls.Config {
	t.Helper()
	pool := x509.NewCertPool()
	cert, err := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
	require.NoError(t, err)
	pool.AddCert(cert)
	return &tls.Config{RootCAs: pool}
}

// serveHTTP2ProbePeer can withhold PING acknowledgements without sending FIN or
// RST, reproducing an upstream connection silently lost by an intermediary.
func serveHTTP2ProbePeer(conn net.Conn, acknowledge bool, pings *atomic.Int32) error {
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(conn, preface); err != nil {
		return err
	}
	if string(preface) != http2.ClientPreface {
		return fmt.Errorf("unexpected HTTP/2 preface: %q", preface)
	}
	framer := http2.NewFramer(conn, conn)
	framer.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	if err := framer.WriteSettings(); err != nil {
		return err
	}
	var streamID uint32
	for {
		frame, err := framer.ReadFrame()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch frame := frame.(type) {
		case *http2.SettingsFrame:
			if !frame.IsAck() {
				if err := framer.WriteSettingsAck(); err != nil {
					return err
				}
			}
		case *http2.MetaHeadersFrame:
			streamID = frame.StreamID
			var block bytes.Buffer
			if err := hpack.NewEncoder(&block).WriteField(hpack.HeaderField{Name: ":status", Value: "200"}); err != nil {
				return err
			}
			if err := framer.WriteHeaders(http2.HeadersFrameParam{StreamID: streamID, BlockFragment: block.Bytes(), EndHeaders: true}); err != nil {
				return err
			}
			if err := framer.WriteData(streamID, false, []byte("start")); err != nil {
				return err
			}
		case *http2.PingFrame:
			if frame.IsAck() {
				continue
			}
			count := pings.Add(1)
			if acknowledge {
				if err := framer.WritePing(true, frame.Data); err != nil {
					return err
				}
				if count >= 2 && streamID != 0 {
					if err := framer.WriteData(streamID, true, []byte("end")); err != nil {
						return err
					}
					streamID = 0
				}
			}
		}
	}
}

func TestRelayHTTP2HealthCheck(t *testing.T) {
	for _, tc := range []struct {
		name        string
		idleTimeout int
		acknowledge bool
	}{
		{name: "disabled", idleTimeout: 0},
		{name: "healthy slow response", idleTimeout: 1, acknowledge: true},
		{name: "unresponsive connection", idleTimeout: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withRelayH2HealthSettings(t)
			common.RelayHTTP2PingIdleTimeout = tc.idleTimeout

			var connections, pings atomic.Int32
			peerErrors := make(chan error, 4)
			server := httptest.NewUnstartedServer(nil)
			server.EnableHTTP2 = true
			server.Config.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){
				"h2": func(_ *http.Server, conn *tls.Conn, _ http.Handler) {
					connection := connections.Add(1)
					acknowledge := tc.acknowledge || connection > 1
					peerErrors <- serveHTTP2ProbePeer(conn, acknowledge, &pings)
				},
			}
			server.StartTLS()
			t.Cleanup(server.Close)
			client := newHTTPClientWithPolicyAndTLS(defaultHTTPTransportPolicy(), testH2TLSClientConfig(t, server))
			client.Timeout = 7 * time.Second
			if tc.idleTimeout == 0 {
				client.Timeout = 3 * time.Second
			}
			t.Cleanup(client.CloseIdleConnections)

			resp, err := client.Get(server.URL)
			require.NoError(t, err)
			require.Equal(t, 2, resp.ProtoMajor)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, resp.Body.Close())
			if tc.idleTimeout == 0 {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				assert.Zero(t, pings.Load(), "disabled probes must preserve the existing transport behavior")
			} else if tc.acknowledge {
				require.NoError(t, err, "a slow response must survive acknowledged health checks")
				assert.Equal(t, "startend", string(body))
				assert.GreaterOrEqual(t, pings.Load(), int32(2))
				assert.Equal(t, int32(1), connections.Load())
			} else {
				require.ErrorContains(t, err, "http2: client connection lost")
				assert.Equal(t, "start", string(body), "already delivered content must not be replayed")
				assert.Equal(t, int32(1), connections.Load())
				require.NoError(t, <-peerErrors, "the peer must observe the failed connection close")

				resp, err = client.Get(server.URL)
				require.NoError(t, err)
				body, err = io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				assert.Equal(t, "startend", string(body))
				assert.Equal(t, int32(2), connections.Load(), "the next request must establish a fresh connection")
			}
		})
	}
}
