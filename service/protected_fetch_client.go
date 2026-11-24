package service

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

type ssrfResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type protectedFetchDialer struct {
	resolver      ssrfResolver
	dialContext   func(ctx context.Context, network, address string) (net.Conn, error)
	getProtection func() (*common.SSRFProtection, bool, error)
}

type ssrfProtectedRoundTripper struct {
	resolver         ssrfResolver
	dialContext      func(ctx context.Context, network, address string) (net.Conn, error)
	getProtection    func() (*common.SSRFProtection, bool, error)
	proxy            func(*http.Request) (*url.URL, error)
	forceVerifiedTLS bool

	mutex      sync.Mutex
	transports map[string]*http.Transport
}

func newAdtyVerifiedTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: false,
		MinVersion:         tls.VersionTLS12,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if cs.Version < tls.VersionTLS12 {
				return ErrAdtyUnverifiedPaymentTLS
			}
			if len(cs.PeerCertificates) == 0 || len(cs.VerifiedChains) == 0 {
				return ErrAdtyUnverifiedPaymentTLS
			}
			return nil
		},
	}
}

var (
	adtyVerifiedDirectTransportOnce sync.Once
	adtyVerifiedDirectTransport     *http.Transport
)

func getAdtyVerifiedDirectTransport() *http.Transport {
	adtyVerifiedDirectTransportOnce.Do(func() {
		dialer := &net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		adtyVerifiedDirectTransport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
			TLSClientConfig:       newAdtyVerifiedTLSConfig(),
		}
	})
	return adtyVerifiedDirectTransport
}

// NewVerifiedDirectNoRedirectHTTPClient builds an outbound HTTP client backed by
// an independent verified-TLS transport (never inheriting DefaultTransport's
// TLS_INSECURE_SKIP_VERIFY mutation) with redirects disabled and a finite timeout (R4-002).
func NewVerifiedDirectNoRedirectHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: getAdtyVerifiedDirectTransport(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func newVerifiedNoRedirectProtectedFetchHTTPClient(timeout time.Duration) *http.Client {
	netDialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &ssrfProtectedRoundTripper{
			resolver:         net.DefaultResolver,
			dialContext:      netDialer.DialContext,
			getProtection:    currentFetchProtection,
			proxy:            http.ProxyFromEnvironment,
			forceVerifiedTLS: true,
			transports:       make(map[string]*http.Transport),
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func currentFetchProtection() (*common.SSRFProtection, bool, error) {
	fetchSetting := system_setting.GetFetchSetting()
	if !fetchSetting.EnableSSRFProtection {
		return nil, false, nil
	}

	protection, err := common.NewSSRFProtectionFromFetchSetting(
		fetchSetting.AllowPrivateIp,
		fetchSetting.DomainFilterMode,
		fetchSetting.IpFilterMode,
		fetchSetting.DomainList,
		fetchSetting.IpList,
		fetchSetting.AllowedPorts,
		fetchSetting.ApplyIPFilterForDomain,
	)
	if err != nil {
		return nil, true, err
	}
	return protection, true, nil
}

func newProtectedFetchHTTPClient() *http.Client {
	return newProtectedFetchHTTPClientWithDialer(nil, nil, nil)
}

func newProtectedFetchHTTPClientWithDialer(resolver ssrfResolver, dialContext func(ctx context.Context, network, address string) (net.Conn, error), getProtection func() (*common.SSRFProtection, bool, error)) *http.Client {
	return newProtectedFetchHTTPClientWithProxy(resolver, dialContext, getProtection, http.ProxyFromEnvironment)
}

func newProtectedFetchHTTPClientWithProxy(resolver ssrfResolver, dialContext func(ctx context.Context, network, address string) (net.Conn, error), getProtection func() (*common.SSRFProtection, bool, error), proxy func(*http.Request) (*url.URL, error)) *http.Client {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if dialContext == nil {
		netDialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		dialContext = netDialer.DialContext
	}
	if getProtection == nil {
		getProtection = currentFetchProtection
	}
	if proxy == nil {
		proxy = http.ProxyFromEnvironment
	}

	client := &http.Client{
		Transport: &ssrfProtectedRoundTripper{
			resolver:      resolver,
			dialContext:   dialContext,
			getProtection: getProtection,
			proxy:         proxy,
			transports:    make(map[string]*http.Transport),
		},
		CheckRedirect: checkProtectedFetchRedirect,
	}
	if common.RelayTimeout != 0 {
		client.Timeout = time.Duration(common.RelayTimeout) * time.Second
	} else {
		client.Timeout = 60 * time.Second
	}
	return client
}

func (t *ssrfProtectedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("invalid request")
	}
	if err := ValidateSSRFProtectedFetchURL(req.URL.String()); err != nil {
		return nil, err
	}

	proxyURL, err := t.proxy(req)
	if err != nil {
		return nil, err
	}
	if proxyURL != nil {
		return t.roundTripViaPinnedProxy(req, proxyURL)
	}
	return t.transportFor(nil).RoundTrip(req)
}

type closeIdleOnBodyClose struct {
	io.ReadCloser
	transport *http.Transport
	once      sync.Once
}

func (c *closeIdleOnBodyClose) Close() error {
	err := c.ReadCloser.Close()
	c.once.Do(func() {
		if c.transport != nil {
			c.transport.CloseIdleConnections()
		}
	})
	return err
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

func dialHTTPSOverHTTPSProxy(ctx context.Context, dialContext func(ctx context.Context, network, address string) (net.Conn, error), proxyURL *url.URL, targetAddr, origHost string, baseTLS *tls.Config) (net.Conn, error) {
	proxyHost := proxyURL.Host
	if proxyURL.Port() == "" {
		proxyHost = net.JoinHostPort(proxyURL.Hostname(), "443")
	}
	rawConn, err := dialContext(ctx, "tcp", proxyHost)
	if err != nil {
		return nil, err
	}
	proxyTLS := baseTLS.Clone()
	proxyTLS.ServerName = proxyURL.Hostname()
	proxyTLSConn := tls.Client(rawConn, proxyTLS)
	if err := proxyTLSConn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	connectReq := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: targetAddr},
		Host:   targetAddr,
		Header: make(http.Header),
	}
	if proxyURL.User != nil {
		auth := base64.StdEncoding.EncodeToString([]byte(proxyURL.User.String()))
		connectReq.Header.Set("Proxy-Authorization", "Basic "+auth)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = proxyTLSConn.SetDeadline(deadline)
	}
	if err := connectReq.Write(proxyTLSConn); err != nil {
		_ = proxyTLSConn.Close()
		return nil, err
	}
	br := bufio.NewReader(proxyTLSConn)
	connectResp, err := http.ReadResponse(br, connectReq)
	if err != nil {
		_ = proxyTLSConn.Close()
		return nil, err
	}
	_ = connectResp.Body.Close()
	if connectResp.StatusCode != http.StatusOK {
		_ = proxyTLSConn.Close()
		return nil, fmt.Errorf("proxy CONNECT failed with status %d", connectResp.StatusCode)
	}
	_ = proxyTLSConn.SetDeadline(time.Time{})
	tunnelConn := &bufferedConn{Conn: proxyTLSConn, r: br}
	targetTLS := baseTLS.Clone()
	targetTLS.ServerName = origHost
	targetTLSConn := tls.Client(tunnelConn, targetTLS)
	if err := targetTLSConn.HandshakeContext(ctx); err != nil {
		_ = proxyTLSConn.Close()
		return nil, err
	}
	return targetTLSConn, nil
}

// roundTripViaPinnedProxy pins domain targets to a validated resolved IP before
// handing the request to an environment proxy, blocking DNS rebinding and
// proxy-side split-horizon internal DNS SSRF while preserving Host and TLS SNI (R10-022/R11-022).
func (t *ssrfProtectedRoundTripper) roundTripViaPinnedProxy(req *http.Request, proxyURL *url.URL) (*http.Response, error) {
	protection, enabled, err := t.getProtection()
	if err != nil {
		return nil, err
	}
	if !enabled || !protection.NeedsDomainIPResolution() {
		return t.transportFor(proxyURL).RoundTrip(req)
	}

	origHost := req.URL.Hostname()
	if _, isIP, ipErr := common.ParseSSRFIPLiteral(origHost); ipErr != nil {
		return nil, ipErr
	} else if isIP {
		return t.transportFor(proxyURL).RoundTrip(req)
	}

	portText := req.URL.Port()
	if portText == "" {
		if strings.EqualFold(req.URL.Scheme, "https") {
			portText = "443"
		} else {
			portText = "80"
		}
	}
	resolved, err := t.resolver.LookupIPAddr(req.Context(), origHost)
	if err != nil {
		return nil, fmt.Errorf("DNS resolution failed for %s: %v", origHost, err)
	}
	var pinnedIP net.IP
	for _, ipAddr := range resolved {
		if ipAddr.IP == nil {
			continue
		}
		if err := protection.ValidateResolvedIP(origHost, ipAddr.IP); err != nil {
			return nil, err
		}
		if pinnedIP == nil {
			pinnedIP = ipAddr.IP
		}
	}
	if pinnedIP == nil {
		return nil, fmt.Errorf("DNS resolution for %s returned no usable IP addresses", origHost)
	}

	pinnedReq := req.Clone(req.Context())
	if pinnedReq.Host == "" {
		pinnedReq.Host = req.URL.Host
	}
	pinnedURL := *req.URL
	pinnedAddr := net.JoinHostPort(pinnedIP.String(), portText)
	pinnedURL.Host = pinnedAddr
	pinnedReq.URL = &pinnedURL

	if !strings.EqualFold(req.URL.Scheme, "https") {
		resp, err := t.transportFor(proxyURL).RoundTrip(pinnedReq)
		if resp != nil {
			resp.Request = req
		}
		return resp, err
	}

	ephemeral := t.newTransport(proxyURL)
	var baseTLS *tls.Config
	if ephemeral.TLSClientConfig != nil {
		baseTLS = ephemeral.TLSClientConfig.Clone()
	} else {
		baseTLS = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if strings.EqualFold(proxyURL.Scheme, "https") {
		ephemeral.Proxy = nil
		ephemeral.TLSClientConfig = nil
		ephemeral.ForceAttemptHTTP2 = false
		ephemeral.DialTLSContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialHTTPSOverHTTPSProxy(ctx, t.dialContext, proxyURL, pinnedAddr, origHost, baseTLS)
		}
	} else {
		baseTLS.ServerName = origHost
		ephemeral.TLSClientConfig = baseTLS
	}
	resp, err := ephemeral.RoundTrip(pinnedReq)
	if err != nil {
		ephemeral.CloseIdleConnections()
		return nil, err
	}
	if resp != nil {
		resp.Request = req
	}
	if resp != nil && resp.Body != nil {
		resp.Body = &closeIdleOnBodyClose{ReadCloser: resp.Body, transport: ephemeral}
	} else {
		ephemeral.CloseIdleConnections()
	}
	return resp, nil
}

func (t *ssrfProtectedRoundTripper) CloseIdleConnections() {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	for _, transport := range t.transports {
		transport.CloseIdleConnections()
	}
}

func (t *ssrfProtectedRoundTripper) transportFor(proxyURL *url.URL) *http.Transport {
	// 只按代理地址分组：代理来自环境变量，取值有限，map 有界；
	// 目标 origin 是用户可控输入，不能作为缓存 key。
	key := "direct"
	if proxyURL != nil {
		key = proxyURL.String()
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if transport, ok := t.transports[key]; ok {
		return transport
	}

	transport := t.newTransport(proxyURL)
	t.transports[key] = transport
	return transport
}

func (t *ssrfProtectedRoundTripper) newTransport(proxyURL *url.URL) *http.Transport {
	dialContext := t.dialContext
	proxyFunc := http.ProxyURL(proxyURL)
	if proxyURL == nil {
		protectedDialer := &protectedFetchDialer{
			resolver:      t.resolver,
			dialContext:   t.dialContext,
			getProtection: t.getProtection,
		}
		dialContext = protectedDialer.DialContext
		proxyFunc = nil
	}

	transport := &http.Transport{
		MaxIdleConns:          common.RelayMaxIdleConns,
		MaxIdleConnsPerHost:   common.RelayMaxIdleConnsPerHost,
		IdleConnTimeout:       time.Duration(common.RelayIdleConnTimeout) * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
		Proxy:                 proxyFunc,
		DialContext:           dialContext,
	}
	if t.forceVerifiedTLS {
		transport.TLSClientConfig = newAdtyVerifiedTLSConfig()
	} else if common.TLSInsecureSkipVerify {
		transport.TLSClientConfig = common.InsecureTLSConfig.Clone()
	}
	return transport
}

func (d *protectedFetchDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	protection, enabled, err := d.getProtection()
	if err != nil {
		return nil, err
	}
	if !enabled {
		return d.dialContext(ctx, network, addr)
	}

	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid dial address %s: %w", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, fmt.Errorf("invalid port: %s", portText)
	}
	if err := protection.ValidateNetworkTarget(host, port); err != nil {
		return nil, err
	}

	if ip, isIP, err := common.ParseSSRFIPLiteral(host); err != nil {
		return nil, err
	} else if isIP {
		return d.dialContext(ctx, network, net.JoinHostPort(ip.String(), portText))
	}
	if !protection.NeedsDomainIPResolution() {
		return d.dialContext(ctx, network, addr)
	}

	resolved, err := d.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("DNS resolution failed for %s: %v", host, err)
	}

	var candidateIPs []net.IP
	for _, ipAddr := range resolved {
		ip := ipAddr.IP
		if ip == nil || !networkAllowsIP(network, ip) {
			continue
		}
		if err := protection.ValidateResolvedIP(host, ip); err != nil {
			return nil, err
		}
		candidateIPs = append(candidateIPs, ip)
	}

	var lastDialErr error
	for _, ip := range candidateIPs {
		conn, err := d.dialContext(ctx, network, net.JoinHostPort(ip.String(), portText))
		if err == nil {
			return conn, nil
		}
		lastDialErr = err
	}

	if lastDialErr != nil {
		return nil, lastDialErr
	}
	return nil, fmt.Errorf("DNS resolution for %s returned no usable IP addresses", host)
}

func networkAllowsIP(network string, ip net.IP) bool {
	switch network {
	case "tcp4":
		return ip.To4() != nil
	case "tcp6":
		return ip.To4() == nil
	default:
		return true
	}
}
