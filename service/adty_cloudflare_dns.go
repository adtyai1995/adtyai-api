package service

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// AdtyCloudflareDNSRecord represents one DNS record managed in Cloudflare DNS.
type AdtyCloudflareDNSRecord struct {
	Id      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

func (c *AdtyCloudflareClient) getZoneName(ctx context.Context, zoneID string) (string, error) {
	zoneID = strings.TrimSpace(zoneID)
	if !adtyCloudflareZoneIDPattern.MatchString(zoneID) {
		return "", common.NewMessage("Invalid Cloudflare zone ID")
	}
	var out struct {
		Id   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(zoneID), nil, &out); err != nil {
		return "", err
	}
	zoneName := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(out.Name)), ".")
	if zoneName == "" {
		return "", common.NewMessage("Cloudflare zone name could not be determined")
	}
	return zoneName, nil
}

func (c *AdtyCloudflareClient) verifyHostInZone(ctx context.Context, zoneID, host string, allowApex bool) (string, error) {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return "", common.NewMessage("DNS record name is required")
	}
	zoneName, err := c.getZoneName(ctx, zoneID)
	if err != nil {
		return "", err
	}
	if host == zoneName {
		if !allowApex {
			return "", common.NewMessage("DNS record must be a subdomain inside the configured Cloudflare zone")
		}
		return zoneName, nil
	}
	if !strings.HasSuffix(host, "."+zoneName) {
		return "", common.NewMessage("DNS record {{host}} is outside the configured Cloudflare zone {{zone}}", map[string]any{
			"host": host,
			"zone": zoneName,
		})
	}
	return zoneName, nil
}

func (c *AdtyCloudflareClient) listDNSRecords(ctx context.Context, zoneID, recordType, name string) ([]AdtyCloudflareDNSRecord, error) {
	query := url.Values{}
	if recordType != "" {
		query.Set("type", recordType)
	}
	if name != "" {
		query.Set("name", strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), "."))
	}
	query.Set("per_page", "100")
	var out []AdtyCloudflareDNSRecord
	path := "/zones/" + url.PathEscape(strings.TrimSpace(zoneID)) + "/dns_records?" + query.Encode()
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *AdtyCloudflareClient) createTXTRecord(ctx context.Context, zoneID, name, value string) (string, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if _, err := c.verifyHostInZone(ctx, zoneID, name, false); err != nil {
		return "", err
	}
	var out AdtyCloudflareDNSRecord
	body := map[string]any{
		"type":    "TXT",
		"name":    name,
		"content": value,
		"ttl":     60,
	}
	if err := c.do(ctx, http.MethodPost, "/zones/"+url.PathEscape(strings.TrimSpace(zoneID))+"/dns_records", body, &out); err != nil {
		return "", err
	}
	if out.Id == "" {
		return "", common.NewMessage("Cloudflare did not return a DNS record ID")
	}
	return out.Id, nil
}

func (c *AdtyCloudflareClient) deleteDNSRecord(ctx context.Context, zoneID, recordID string) error {
	recordID = strings.TrimSpace(recordID)
	if recordID == "" {
		return nil
	}
	err := c.do(ctx, http.MethodDelete, "/zones/"+url.PathEscape(strings.TrimSpace(zoneID))+"/dns_records/"+url.PathEscape(recordID), nil, nil)
	var apiErr *AdtyCloudflareError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return nil
	}
	return err
}

func (c *AdtyCloudflareClient) upsertARecord(ctx context.Context, zoneID, name, ip string, proxied bool) (*AdtyCloudflareDNSRecord, error) {
	records, err := c.upsertARecords(ctx, zoneID, name, []string{ip}, proxied)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, common.NewMessage("Cloudflare did not return an A record")
	}
	return &records[0], nil
}

// upsertARecords reconciles the exact set of Cloudflare DNS A records for name.
// In orange-cloud proxy mode (proxied=true), Cloudflare SaaS routes custom hostnames
// to the fallback origin so a single proxied A record with auto TTL (1) is maintained.
// In direct DNS-only mode (proxied=false), one TTL=60 A record per unique IP is kept
// in place without unnecessary delete/recreate churn.
func (c *AdtyCloudflareClient) upsertARecords(ctx context.Context, zoneID, name string, ips []string, proxied bool) ([]AdtyCloudflareDNSRecord, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if _, err := c.verifyHostInZone(ctx, zoneID, name, false); err != nil {
		return nil, err
	}
	desired := make([]string, 0, len(ips))
	seen := make(map[string]struct{}, len(ips))
	for _, raw := range ips {
		ip := strings.TrimSpace(raw)
		if ip == "" {
			continue
		}
		if _, ok := seen[ip]; ok {
			continue
		}
		seen[ip] = struct{}{}
		desired = append(desired, ip)
	}
	if len(desired) == 0 {
		return nil, common.NewMessage("At least one edge node IP is required")
	}
	ttl := 60
	if proxied {
		ttl = 1
		desired = desired[:1]
	}

	existing, err := c.listDNSRecords(ctx, zoneID, "A", name)
	if err != nil {
		return nil, err
	}

	matchedByIP := make(map[string]AdtyCloudflareDNSRecord, len(desired))
	unmatched := make([]AdtyCloudflareDNSRecord, 0, len(existing))
	for _, rec := range existing {
		ip := strings.TrimSpace(rec.Content)
		if _, want := seen[ip]; want {
			if _, already := matchedByIP[ip]; !already && (!proxied || ip == desired[0]) {
				matchedByIP[ip] = rec
				continue
			}
		}
		unmatched = append(unmatched, rec)
	}

	reconciled := make([]AdtyCloudflareDNSRecord, 0, len(desired))
	zonePath := "/zones/" + url.PathEscape(strings.TrimSpace(zoneID)) + "/dns_records"
	for _, ip := range desired {
		body := map[string]any{
			"type":    "A",
			"name":    name,
			"content": ip,
			"ttl":     ttl,
			"proxied": proxied,
		}
		if rec, ok := matchedByIP[ip]; ok {
			if rec.Proxied == proxied && (proxied || rec.TTL == ttl) {
				reconciled = append(reconciled, rec)
				continue
			}
			var updated AdtyCloudflareDNSRecord
			if err := c.do(ctx, http.MethodPut, zonePath+"/"+url.PathEscape(rec.Id), body, &updated); err != nil {
				return nil, err
			}
			reconciled = append(reconciled, updated)
			continue
		}
		if len(unmatched) > 0 {
			reuse := unmatched[0]
			unmatched = unmatched[1:]
			var updated AdtyCloudflareDNSRecord
			if err := c.do(ctx, http.MethodPut, zonePath+"/"+url.PathEscape(reuse.Id), body, &updated); err != nil {
				return nil, err
			}
			reconciled = append(reconciled, updated)
			continue
		}
		var created AdtyCloudflareDNSRecord
		if err := c.do(ctx, http.MethodPost, zonePath, body, &created); err != nil {
			return nil, err
		}
		reconciled = append(reconciled, created)
	}

	for _, extra := range unmatched {
		if err := c.deleteDNSRecord(ctx, zoneID, extra.Id); err != nil {
			return reconciled, err
		}
	}
	return reconciled, nil
}
