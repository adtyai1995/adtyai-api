package service

import (
	"context"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"net"
	"net/http"
	"strings"
	"time"
)

// Resolve and validate at dial time; redirects are not followed and the normal
// TLS verifier checks the actual hostname against the edge certificate.
var adtyProbeDomainHTTPS = func(ctx context.Context, domain *model.AdtySiteDomain) error {
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("Domain DNS lookup failed")
		}
		guard := &common.SSRFProtection{AllowPrivateIp: false, IpFilterMode: false}
		for _, ip := range ips {
			if !ip.IP.IsGlobalUnicast() || !guard.IsIPAccessAllowed(ip.IP) {
				return nil, fmt.Errorf("Domain resolves to a restricted network")
			}
		}
		var last error
		for _, ip := range ips {
			conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		if last == nil {
			last = fmt.Errorf("Domain has no addresses")
		}
		return nil, last
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	path := "/"
	if domain.Role == model.AdtySiteDomainRoleBaseURL {
		path = "/api/status"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain.Hostname+path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Domain HTTPS verification failed")
	}
	resp.Body.Close()
	if domain.DesiredState == "redirect_only" {
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != strings.TrimRight(AdtySiteBaseURL(nil), "/")+"/" {
			return fmt.Errorf("Closed website does not redirect to the main site")
		}
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Domain returned HTTP %d", resp.StatusCode)
	}
	return nil
}
