package service

import (
	"context"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"io"
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
	path := AdtyDomainProbePath
	if domain.DesiredState == "redirect_only" {
		path = "/"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain.Hostname+path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Domain HTTPS verification failed")
	}
	defer resp.Body.Close()
	return checkAdtyDomainProbeResponse(resp, domain)
}

func checkAdtyDomainProbeResponse(resp *http.Response, domain *model.AdtySiteDomain) error {
	if domain.DesiredState == "redirect_only" {
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != strings.TrimRight(AdtySiteBaseURL(nil), "/")+"/" {
			return fmt.Errorf("Closed website does not redirect to the main site")
		}
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Domain returned HTTP %d", resp.StatusCode)
	}
	var identity AdtyDomainProbeIdentity
	if err := common.DecodeJson(io.LimitReader(resp.Body, 2048), &identity); err != nil || identity.SiteID != domain.SiteId || identity.Role != domain.Role || identity.Hostname != domain.Hostname {
		return fmt.Errorf("Domain points to a different site or endpoint")
	}
	return nil
}

const AdtyDomainProbePath = "/.well-known/adty-site-binding"

type AdtyDomainProbeIdentity struct {
	SiteID   int    `json:"site_id"`
	Role     string `json:"role"`
	Hostname string `json:"hostname"`
}

// Answer only for the hostname received over HTTPS. A pending binding is
// eligible for this one probe before normal pages or relay traffic are enabled.
func AdtyDomainProbeForHost(host string) (*AdtyDomainProbeIdentity, error) {
	var row model.AdtySiteDomain
	err := model.DB.Model(&model.AdtySiteDomain{}).Joins("JOIN adty_sites ON adty_sites.id = adty_site_domains.site_id").
		Where("adty_site_domains.hostname = ? AND desired_state = ? AND adty_site_domains.status IN ? AND adty_sites.status IN ?", host, "serve", []string{"pending", "active"}, []string{"active", "closing", "paused"}).First(&row).Error
	if err != nil {
		return nil, err
	}
	return &AdtyDomainProbeIdentity{row.SiteId, row.Role, row.Hostname}, nil
}
