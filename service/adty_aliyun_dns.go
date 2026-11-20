package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AdtyDNSRecord identifies one provider-owned record, including its routing line.
type AdtyDNSRecord struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
	Line  string `json:"line"`
}

// Find returns exact matching values; callers decide whether a persisted
// creation intent authorizes adoption. Never adopt a manual record by name alone.
func (c *AdtyAliDNSClient) Find(ctx context.Context, root string, wanted AdtyDNSRecord) (*AdtyDNSRecord, error) {
	rr, err := adtyDNSRelativeName(root, wanted.Name)
	if err != nil {
		return nil, err
	}
	for page := 1; page <= 100; page++ {
		out, err := c.call(ctx, "DescribeDomainRecords", url.Values{"DomainName": {root}, "RRKeyWord": {rr}, "SearchMode": {"EXACT"}, "PageNumber": {fmt.Sprint(page)}, "PageSize": {"100"}})
		if err != nil {
			return nil, err
		}
		for _, record := range out.DomainRecords.Record {
			if record.RR == rr && record.Type == wanted.Type && record.Line == wanted.Line && record.Value == wanted.Value {
				found := wanted
				found.ID = record.RecordID
				return &found, nil
			}
		}
		if len(out.DomainRecords.Record) < 100 {
			return nil, nil
		}
	}
	return nil, fmt.Errorf("AliDNS record search exceeded its page limit")
}

type AdtyAliDNSClient struct {
	Endpoint        string
	HTTP            *http.Client
	AccessKeyID     string
	AccessKeySecret string
}

type adtyAliDNSResponse struct {
	RecordID      string `json:"RecordId"`
	Code          string `json:"Code"`
	DomainName    string `json:"DomainName"`
	RR            string `json:"RR"`
	Type          string `json:"Type"`
	Value         string `json:"Value"`
	Line          string `json:"Line"`
	DomainRecords struct {
		Record []struct {
			RecordID string `json:"RecordId"`
			RR       string `json:"RR"`
			Type     string `json:"Type"`
			Value    string `json:"Value"`
			Line     string `json:"Line"`
		}
	} `json:"DomainRecords"`
}

func (c *AdtyAliDNSClient) call(ctx context.Context, action string, params url.Values) (*adtyAliDNSResponse, error) {
	if c.AccessKeyID == "" || c.AccessKeySecret == "" {
		return nil, fmt.Errorf("AliDNS credentials are not configured")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	params.Set("Action", action)
	params.Set("Format", "JSON")
	params.Set("Version", "2015-01-09")
	params.Set("AccessKeyId", c.AccessKeyID)
	params.Set("SignatureMethod", "HMAC-SHA1")
	params.Set("SignatureVersion", "1.0")
	params.Set("SignatureNonce", hex.EncodeToString(nonce))
	params.Set("Timestamp", time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	canonical := strings.ReplaceAll(params.Encode(), "+", "%20")
	escape := func(v string) string { return strings.ReplaceAll(url.QueryEscape(v), "+", "%20") }
	mac := hmac.New(sha1.New, []byte(c.AccessKeySecret+"&"))
	_, _ = mac.Write([]byte("POST&%2F&" + escape(canonical)))
	params.Set("Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = "https://alidns.aliyuncs.com/"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("AliDNS request could not be created")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("AliDNS request failed")
	}
	defer resp.Body.Close()
	var out adtyAliDNSResponse
	if err := common.DecodeJson(io.LimitReader(resp.Body, 2<<20), &out); err != nil {
		return nil, fmt.Errorf("AliDNS returned an invalid response")
	}
	if out.Code != "" || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &out, fmt.Errorf("AliDNS %s: HTTP %d", out.Code, resp.StatusCode)
	}
	return &out, nil
}

func adtyDNSRelativeName(root, name string) (string, error) {
	root = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(root)), ".")
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if root == "" || !strings.HasSuffix(name, "."+root) {
		return "", fmt.Errorf("DNS record is outside the configured root domain")
	}
	rr := strings.TrimSuffix(name, "."+root)
	if rr == "" || strings.ContainsAny(rr, "*/ \r\n") {
		return "", fmt.Errorf("Invalid managed DNS record name")
	}
	return rr, nil
}

func (c *AdtyAliDNSClient) Add(ctx context.Context, root string, record AdtyDNSRecord) (AdtyDNSRecord, error) {
	rr, err := adtyDNSRelativeName(root, record.Name)
	if err != nil {
		return record, err
	}
	if record.Type != "TXT" && record.Type != "CNAME" {
		return record, fmt.Errorf("Unsupported managed DNS record type")
	}
	if record.Line != "default" && record.Line != "internal" && record.Line != "oversea" {
		return record, fmt.Errorf("Unsupported DNS routing line")
	}
	out, err := c.call(ctx, "AddDomainRecord", url.Values{"DomainName": {root}, "RR": {rr}, "Type": {record.Type}, "Value": {record.Value}, "Line": {record.Line}, "TTL": {"600"}})
	if err != nil {
		return record, err
	}
	if out.RecordID == "" {
		return record, fmt.Errorf("AliDNS did not return a record ID")
	}
	record.ID = out.RecordID
	return record, nil
}

func (c *AdtyAliDNSClient) Delete(ctx context.Context, root string, record AdtyDNSRecord) error {
	rr, err := adtyDNSRelativeName(root, record.Name)
	if err != nil {
		return err
	}
	if record.ID == "" {
		return fmt.Errorf("DNS record ID is missing")
	}
	out, err := c.call(ctx, "DescribeDomainRecordInfo", url.Values{"RecordId": {record.ID}})
	if out != nil && out.Code == "InvalidRecordId.NotFound" {
		return nil
	}
	if err != nil {
		return err
	}
	if out.DomainName != root || out.RR != rr || out.Type != record.Type || out.Value != record.Value || out.Line != record.Line {
		return fmt.Errorf("DNS record changed outside automation; deletion refused")
	}
	out, err = c.call(ctx, "DeleteDomainRecord", url.Values{"RecordId": {record.ID}})
	if out != nil && out.Code == "InvalidRecordId.NotFound" {
		return nil
	}
	return err
}

func (c *AdtyAliDNSClient) Check(ctx context.Context, root string) error {
	_, err := c.call(ctx, "DescribeDomainInfo", url.Values{"DomainName": {root}})
	return err
}

// Update changes only a recorded CNAME. An already-applied value is a safe
// retry after a lost response; unrelated manual changes stop reconciliation.
func (c *AdtyAliDNSClient) Update(ctx context.Context, root string, old AdtyDNSRecord, value string) (AdtyDNSRecord, error) {
	rr, err := adtyDNSRelativeName(root, old.Name)
	if err != nil {
		return old, err
	}
	if old.ID == "" || old.Type != "CNAME" {
		return old, fmt.Errorf("Only recorded CNAME targets can be updated")
	}
	out, err := c.call(ctx, "DescribeDomainRecordInfo", url.Values{"RecordId": {old.ID}})
	if err != nil {
		return old, err
	}
	if out.DomainName != root || out.RR != rr || out.Type != old.Type || out.Line != old.Line || (out.Value != old.Value && out.Value != value) {
		return old, fmt.Errorf("DNS record changed outside automation; update refused")
	}
	if out.Value != value {
		_, err = c.call(ctx, "UpdateDomainRecord", url.Values{"RecordId": {old.ID}, "RR": {rr}, "Type": {old.Type}, "Value": {value}, "Line": {old.Line}, "TTL": {"600"}})
		if err != nil {
			return old, err
		}
	}
	old.Value = value
	return old, nil
}
