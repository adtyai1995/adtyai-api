package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupEdgeTestEnv(t *testing.T, configure func(*adty_setting.ResellerSetting)) *gorm.DB {
	t.Helper()
	t.Setenv("CRYPTO_SECRET", "0123456789abcdef0123456789abcdef")

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Option{},
		&model.AdtySite{},
		&model.AdtySiteDomain{},
		&model.AdtySiteAudit{},
		&model.AdtyDomainCloudResource{},
		&model.AdtyEdgeCert{},
		&model.AdtyEdgeACMEAccount{},
		&model.AdtyEdgeState{},
	))

	oldDB := model.DB
	oldCF := AdtyCloudflare
	oldAli := AdtyAliDNS
	oldProbe := adtyProbeDomainHTTPS
	oldCNAME := adtyEdgeLookupCNAME
	oldTXT := adtyEdgeLookupTXT
	oldSleep := adtyEdgeSleep
	oldWait := adtyEdgeTXTPropagateWait
	oldPolls := adtyEdgeTXTPropagatePolls
	oldDir := adtyEdgeACMEDirectoryURL
	oldACMEHTTP := adtyEdgeACMEHTTPClient
	oldEdgeDial := adtyEdgeProbeDialAddr
	oldEdgeRoots := adtyEdgeProbeRootCAs

	model.DB = db
	adtyEdgeSleep = func(time.Duration) {}
	adtyEdgeTXTPropagateWait = 0
	adtyEdgeTXTPropagatePolls = 1
	adtyProbeDomainHTTPS = func(context.Context, *model.AdtySiteDomain) error { return nil }

	rsPtr, ok := config.GlobalConfig.Get(adty_setting.ResellerSettingModule).(*adty_setting.ResellerSetting)
	require.True(t, ok && rsPtr != nil)
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	oldRS := *rsPtr
	nextRS := adty_setting.DefaultResellerSetting()
	nextRS.Enabled = true
	nextRS.CustomDomainEnabled = true
	nextRS.DomainAutomationEnabled = true
	nextRS.EdgeMainlandEnabled = true
	nextRS.CloudflareApiToken = "cf-test-token"
	nextRS.CloudflareZoneId = "zone-adtyaicf"
	nextRS.AliyunAccessKeyID = "ali-id"
	nextRS.AliyunAccessKeySecret = "ali-secret"
	if configure != nil {
		configure(&nextRS)
	}
	*rsPtr = nextRS
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		model.DB = oldDB
		AdtyCloudflare = oldCF
		AdtyAliDNS = oldAli
		adtyProbeDomainHTTPS = oldProbe
		adtyEdgeLookupCNAME = oldCNAME
		adtyEdgeLookupTXT = oldTXT
		adtyEdgeSleep = oldSleep
		adtyEdgeTXTPropagateWait = oldWait
		adtyEdgeTXTPropagatePolls = oldPolls
		adtyEdgeACMEDirectoryURL = oldDir
		adtyEdgeACMEHTTPClient = oldACMEHTTP
		adtyEdgeProbeDialAddr = oldEdgeDial
		adtyEdgeProbeRootCAs = oldEdgeRoots
		common.OptionMapRWMutex.Lock()
		*rsPtr = oldRS
		common.OptionMapRWMutex.Unlock()
	})
	return db
}

func TestAdtyEdgeDefaultsAndValidation(t *testing.T) {
	def := adty_setting.DefaultResellerSetting()
	assert.Equal(t, []string{"adtyai-fz.top"}, def.RootDomains)
	assert.Equal(t, "adtyai-fz.top", def.AliyunDomain)
	assert.Equal(t, "cn.adtyaicf.top", def.DomainMainlandTarget)
	assert.Equal(t, "edge.adtyaicf.top", def.CnameTarget)
	assert.Equal(t, "origin.adtyaicf.top", def.FallbackOrigin)
	assert.Equal(t, "acme.adtyaicf.top", def.EdgeACMEAliasSuffix)
	assert.Empty(t, def.CloudflareZoneId)
	assert.Empty(t, def.CloudflareAccountId)
	assert.Empty(t, def.CloudflareApiToken)
	assert.Empty(t, def.AliyunAccessKeyID)
	assert.Empty(t, def.AliyunAccessKeySecret)
	assert.False(t, def.DomainAutomationEnabled)
	assert.False(t, def.EdgeMainlandEnabled)
	assert.False(t, def.EdgeACMEStaging)
	assert.False(t, def.EdgeFailoverToCF)
	assert.False(t, def.EdgeProbeEnabled)

	// Domain automation allows empty domain_mainland_target.
	cfg := adty_setting.DefaultResellerSetting()
	cfg.DomainAutomationEnabled = true
	cfg.CloudflareApiToken = "tok"
	cfg.CloudflareZoneId = "zone123"
	cfg.AliyunAccessKeyID = "id"
	cfg.AliyunAccessKeySecret = "sec"
	cfg.DomainMainlandTarget = ""
	require.NoError(t, adty_setting.ValidateResellerPatch(&cfg, false, map[string]string{"domain_automation_enabled": "true", "domain_mainland_target": ""}))

	// Edge node IP validation rejects private/loopback/CGNAT and accepts public IPv4.
	assert.False(t, adty_setting.ValidPublicEdgeIPv4("127.0.0.1"))
	assert.False(t, adty_setting.ValidPublicEdgeIPv4("10.0.0.1"))
	assert.False(t, adty_setting.ValidPublicEdgeIPv4("100.64.0.1"))
	assert.False(t, adty_setting.ValidPublicEdgeIPv4("192.168.1.1"))
	assert.False(t, adty_setting.ValidPublicEdgeIPv4("2001:db8::1"))
	assert.True(t, adty_setting.ValidPublicEdgeIPv4("47.242.10.20"))

	cfg.EdgeNodeIP = "10.0.0.5"
	require.Error(t, adty_setting.ValidateResellerPatch(&cfg, false, map[string]string{"edge_node_ip": "10.0.0.5"}))
	cfg.EdgeNodeIP = "47.242.10.20"
	require.NoError(t, adty_setting.ValidateResellerPatch(&cfg, false, map[string]string{"edge_node_ip": "47.242.10.20"}))

	// Multi-node IP list validation, deduplication, legacy fallback, and auto-failover dependency.
	assert.Equal(t, []string{"47.242.10.20"}, cfg.EffectiveEdgeNodeIPs())
	cfg.EdgeNodeIPs = []string{"47.242.10.20", "47.242.10.21", "47.242.10.20"}
	assert.Equal(t, []string{"47.242.10.20", "47.242.10.21"}, cfg.EffectiveEdgeNodeIPs())
	require.NoError(t, adty_setting.ValidateResellerPatch(&cfg, false, map[string]string{"edge_node_ips": `["47.242.10.20","47.242.10.21"]`}))
	cfg.EdgeNodeIPs = []string{"47.242.10.20", "10.0.0.8"}
	require.Error(t, adty_setting.ValidateResellerPatch(&cfg, false, map[string]string{"edge_node_ips": `["47.242.10.20","10.0.0.8"]`}))
	cfg.EdgeNodeIPs = []string{"47.242.10.20", "47.242.10.21"}
	cfg.EdgeAutoFailover = true
	cfg.EdgeProbeEnabled = false
	require.Error(t, adty_setting.ValidateResellerPatch(&cfg, false, map[string]string{"edge_auto_failover": "true"}))
	cfg.EdgeProbeEnabled = true
	require.NoError(t, adty_setting.ValidateResellerPatch(&cfg, false, map[string]string{"edge_auto_failover": "true", "edge_probe_enabled": "true"}))

	// Enabling edge_mainland_enabled requires mainland target + acme suffix + CF.
	cfg.EdgeMainlandEnabled = true
	cfg.DomainMainlandTarget = ""
	require.Error(t, adty_setting.ValidateResellerPatch(&cfg, false, map[string]string{"edge_mainland_enabled": "true"}))
	cfg.DomainMainlandTarget = "cn.adtyaicf.top"
	require.NoError(t, adty_setting.ValidateResellerPatch(&cfg, false, map[string]string{"edge_mainland_enabled": "true"}))
}

type fakeAliDNSStore struct {
	mu      sync.Mutex
	nextID  int
	records map[string]AdtyDNSRecord
}

func writeJSON(w http.ResponseWriter, v any) {
	data, _ := common.Marshal(v)
	_, _ = w.Write(data)
}

func newFakeAliDNSServer(t *testing.T) (*httptest.Server, *fakeAliDNSStore) {
	t.Helper()
	store := &fakeAliDNSStore{records: map[string]AdtyDNSRecord{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		q := r.Form
		action := q.Get("Action")
		store.mu.Lock()
		defer store.mu.Unlock()
		switch action {
		case "DescribeDomainRecords":
			domain := q.Get("DomainName")
			rr := q.Get("RRKeyWord")
			typ := q.Get("TypeKeyWord")
			var list []map[string]string
			for _, rec := range store.records {
				recRR := strings.TrimSuffix(rec.Name, "."+domain)
				if (rr == "" || recRR == rr) && (typ == "" || rec.Type == typ) {
					list = append(list, map[string]string{
						"RecordId": rec.ID,
						"RR":       recRR,
						"Type":     rec.Type,
						"Value":    rec.Value,
						"Line":     rec.Line,
					})
				}
			}
			writeJSON(w, map[string]any{
				"DomainRecords": map[string]any{"Record": list},
			})
		case "AddDomainRecord":
			store.nextID++
			id := fmt.Sprintf("rec-%d", store.nextID)
			domain := q.Get("DomainName")
			rec := AdtyDNSRecord{
				ID:    id,
				Type:  q.Get("Type"),
				Name:  q.Get("RR") + "." + domain,
				Value: q.Get("Value"),
				Line:  q.Get("Line"),
			}
			store.records[id] = rec
			writeJSON(w, map[string]any{"RecordId": id})
		case "UpdateDomainRecord":
			id := q.Get("RecordId")
			rec := store.records[id]
			rec.Value = q.Get("Value")
			store.records[id] = rec
			writeJSON(w, map[string]any{"RecordId": id})
		case "DescribeDomainRecordInfo":
			id := q.Get("RecordId")
			rec, ok := store.records[id]
			if !ok {
				writeJSON(w, map[string]any{"Code": "InvalidRecordId.NotFound"})
				return
			}
			rr, domain, _ := strings.Cut(rec.Name, ".")
			writeJSON(w, map[string]any{
				"RecordId":   rec.ID,
				"DomainName": domain,
				"RR":         rr,
				"Type":       rec.Type,
				"Value":      rec.Value,
				"Line":       rec.Line,
			})
		case "DeleteDomainRecord":
			delete(store.records, q.Get("RecordId"))
			writeJSON(w, map[string]any{"RecordId": q.Get("RecordId")})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, store
}

func TestAdtyPhase1AliDNSSplitAndPruning(t *testing.T) {
	db := setupEdgeTestEnv(t, nil)
	aliSrv, aliStore := newFakeAliDNSServer(t)
	AdtyAliDNS = func() *AdtyAliDNSClient {
		return &AdtyAliDNSClient{
			Endpoint:        aliSrv.URL,
			AccessKeyID:     "ali-id",
			AccessKeySecret: "ali-secret",
			HTTP:            aliSrv.Client(),
		}
	}

	cfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.RawQuery, "hostname="):
			writeJSON(w, map[string]any{"success": true, "result": []any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/custom_hostnames"):
			var body map[string]any
			_ = common.DecodeJson(r.Body, &body)
			writeJSON(w, map[string]any{
				"success": true,
				"result": map[string]any{
					"id":       "cf-" + fmt.Sprint(body["hostname"]),
					"hostname": body["hostname"],
					"status":   "active",
					"ssl":      map[string]any{"status": "active"},
				},
			})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/custom_hostnames/"):
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			host := strings.TrimPrefix(id, "cf-")
			writeJSON(w, map[string]any{
				"success": true,
				"result": map[string]any{
					"id":       id,
					"hostname": host,
					"status":   "active",
					"ssl":      map[string]any{"status": "active"},
					"ownership_verification": map[string]any{
						"name":  "_cf-custom-hostname." + host,
						"type":  "txt",
						"value": "cf-verify-val",
					},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(cfSrv.Close)
	AdtyCloudflare = &AdtyCloudflareClient{
		BaseURL: cfSrv.URL,
		HTTP:    cfSrv.Client(),
		Token:   func() string { return "cf-test-token" },
	}

	site := model.AdtySite{
		OwnerUserId: 10,
		Subdomain:   "shop1",
		RootDomain:  "adtyai-fz.top",
		Status:      model.AdtySiteStatusActive,
	}
	require.NoError(t, db.Create(&site).Error)
	require.NoError(t, adtyAllocatePlatformDomainsTx(db, &site))

	var rows []model.AdtySiteDomain
	require.NoError(t, db.Where("site_id = ?", site.Id).Order("id ASC").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.Equal(t, model.AdtySiteDomainRoleSite, rows[0].Role)
	assert.Equal(t, "cn.adtyaicf.top", rows[0].MainlandTarget)
	assert.Equal(t, model.AdtySiteDomainRoleBaseURL, rows[1].Role)
	assert.Empty(t, rows[1].MainlandTarget)

	// Seed a legacy oversea + internal CNAME on base_url to verify pruning while keeping TXT.
	legacyAPI := []AdtyDNSRecord{
		{ID: "legacy-oversea", Type: "CNAME", Name: rows[1].Hostname, Value: "edge.adtyaicf.top", Line: "oversea"},
		{ID: "legacy-internal", Type: "CNAME", Name: rows[1].Hostname, Value: "cn.adtyaicf.top", Line: "internal"},
	}
	aliStore.mu.Lock()
	for _, r := range legacyAPI {
		aliStore.records[r.ID] = r
	}
	aliStore.mu.Unlock()
	encLegacy, _ := common.Marshal(legacyAPI)
	require.NoError(t, db.Model(&model.AdtySiteDomain{}).Where("id = ?", rows[1].Id).Update("dns_records", string(encLegacy)).Error)

	require.NoError(t, RunAdtyDomainAutomationOnce(context.Background(), time.Now()))

	aliStore.mu.Lock()
	siteLines := map[string]string{}
	apiLines := map[string]string{}
	txtCount := 0
	for _, rec := range aliStore.records {
		if rec.Type == "TXT" {
			txtCount++
			continue
		}
		if rec.Name == rows[0].Hostname {
			siteLines[rec.Line] = rec.Value
		}
		if rec.Name == rows[1].Hostname {
			apiLines[rec.Line] = rec.Value
		}
	}
	aliStore.mu.Unlock()

	assert.Equal(t, map[string]string{"default": "edge.adtyaicf.top", "internal": "cn.adtyaicf.top"}, siteLines)
	assert.Equal(t, map[string]string{"default": "edge.adtyaicf.top"}, apiLines)
	assert.Equal(t, 2, txtCount, "CF ownership TXT records must be preserved")

	// Clearing domain_mainland_target prunes internal CNAME on site domain on next reconcile.
	rsPtr := config.GlobalConfig.Get(adty_setting.ResellerSettingModule).(*adty_setting.ResellerSetting)
	common.OptionMapRWMutex.Lock()
	rsPtr.DomainMainlandTarget = ""
	common.OptionMapRWMutex.Unlock()
	require.NoError(t, db.Model(&model.AdtySiteDomain{}).Where("id = ?", rows[0].Id).Update("next_run_at", 0).Error)

	require.NoError(t, RunAdtyDomainAutomationOnce(context.Background(), time.Now().Add(time.Minute)))

	aliStore.mu.Lock()
	siteLinesAfter := map[string]string{}
	for _, rec := range aliStore.records {
		if rec.Type == "CNAME" && rec.Name == rows[0].Hostname {
			siteLinesAfter[rec.Line] = rec.Value
		}
	}
	aliStore.mu.Unlock()
	assert.Equal(t, map[string]string{"default": "edge.adtyaicf.top"}, siteLinesAfter)
}

type mockACMEServer struct {
	server        *httptest.Server
	caCert        *x509.Certificate
	caKey         *ecdsa.PrivateKey
	mu            sync.Mutex
	orderCount    int
	failFinalize  bool
	certValidDays int
	orders        map[string]string // orderID -> dnsName
}

func newMockACMEServer(t *testing.T) *mockACMEServer {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Mock ACME Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	m := &mockACMEServer{
		caCert:        caCert,
		caKey:         caKey,
		certValidDays: 90,
		orders:        map[string]string{},
	}

	decodeJWS := func(r *http.Request) ([]byte, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		var env struct {
			Payload string `json:"payload"`
		}
		if err := common.Unmarshal(body, &env); err != nil {
			return nil, err
		}
		if env.Payload == "" {
			return nil, nil
		}
		return base64.RawURLEncoding.DecodeString(env.Payload)
	}

	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Replay-Nonce", "test-nonce-12345")
		w.Header().Set("Content-Type", "application/json")
		base := srv.URL
		switch {
		case r.URL.Path == "/directory":
			writeJSON(w, map[string]string{
				"newNonce":   base + "/new-nonce",
				"newAccount": base + "/new-acct",
				"newOrder":   base + "/new-order",
			})
		case r.URL.Path == "/new-nonce":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/new-acct":
			payload, _ := decodeJWS(r)
			var acctReq struct {
				OnlyReturnExisting bool `json:"onlyReturnExisting"`
			}
			_ = common.Unmarshal(payload, &acctReq)
			w.Header().Set("Location", base+"/acct/1")
			if acctReq.OnlyReturnExisting {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusCreated)
			}
			writeJSON(w, map[string]any{"status": "valid"})
		case r.URL.Path == "/new-order":
			payload, _ := decodeJWS(r)
			var req struct {
				Identifiers []struct {
					Type  string `json:"type"`
					Value string `json:"value"`
				} `json:"identifiers"`
			}
			_ = common.Unmarshal(payload, &req)
			m.mu.Lock()
			m.orderCount++
			id := fmt.Sprintf("%d", m.orderCount)
			dnsName := ""
			if len(req.Identifiers) > 0 {
				dnsName = req.Identifiers[0].Value
			}
			m.orders[id] = dnsName
			m.mu.Unlock()
			w.Header().Set("Location", base+"/order/"+id)
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, map[string]any{
				"status":         "pending",
				"identifiers":    req.Identifiers,
				"authorizations": []string{base + "/authz/" + id},
				"finalize":       base + "/finalize/" + id,
			})
		case strings.HasPrefix(r.URL.Path, "/authz/"):
			id := strings.TrimPrefix(r.URL.Path, "/authz/")
			m.mu.Lock()
			dnsName := m.orders[id]
			m.mu.Unlock()
			writeJSON(w, map[string]any{
				"status":     "valid",
				"identifier": map[string]string{"type": "dns", "value": strings.TrimPrefix(dnsName, "*.")},
				"challenges": []map[string]any{
					{"type": "dns-01", "status": "pending", "url": base + "/chal/" + id, "token": "tok-" + id},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/chal/"):
			id := strings.TrimPrefix(r.URL.Path, "/chal/")
			writeJSON(w, map[string]any{
				"type": "dns-01", "status": "valid", "url": base + "/chal/" + id, "token": "tok-" + id,
			})
		case strings.HasPrefix(r.URL.Path, "/order/"):
			id := strings.TrimPrefix(r.URL.Path, "/order/")
			writeJSON(w, map[string]any{
				"status":         "ready",
				"authorizations": []string{base + "/authz/" + id},
				"finalize":       base + "/finalize/" + id,
				"certificate":    base + "/cert/" + id,
			})
		case strings.HasPrefix(r.URL.Path, "/finalize/"):
			id := strings.TrimPrefix(r.URL.Path, "/finalize/")
			m.mu.Lock()
			fail := m.failFinalize
			m.mu.Unlock()
			if fail {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]any{"type": "urn:ietf:params:acme:error:badCSR", "detail": "mock finalize error"})
				return
			}
			_, _ = decodeJWS(r)
			writeJSON(w, map[string]any{
				"status":         "valid",
				"authorizations": []string{base + "/authz/" + id},
				"finalize":       base + "/finalize/" + id,
				"certificate":    base + "/cert/" + id,
			})
		case strings.HasPrefix(r.URL.Path, "/cert/"):
			id := strings.TrimPrefix(r.URL.Path, "/cert/")
			m.mu.Lock()
			dnsName := m.orders[id]
			days := m.certValidDays
			m.mu.Unlock()
			leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			leafTmpl := &x509.Certificate{
				SerialNumber: big.NewInt(time.Now().UnixNano()),
				Subject:      pkix.Name{CommonName: dnsName},
				DNSNames:     []string{dnsName},
				NotBefore:    time.Now().Add(-time.Minute),
				NotAfter:     time.Now().Add(time.Duration(days) * 24 * time.Hour),
				KeyUsage:     x509.KeyUsageDigitalSignature,
				ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			}
			leafDER, _ := x509.CreateCertificate(rand.Reader, leafTmpl, m.caCert, &leafKey.PublicKey, m.caKey)
			w.Header().Set("Content-Type", "application/pem-certificate-chain")
			_ = pem.Encode(w, &pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
			_ = pem.Encode(w, &pem.Block{Type: "CERTIFICATE", Bytes: m.caCert.Raw})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	m.server = srv
	t.Cleanup(srv.Close)
	return m
}

func TestAdtyPhase2ACMEIssuanceRenewalAndManifest(t *testing.T) {
	db := setupEdgeTestEnv(t, nil)
	acmeSrv := newMockACMEServer(t)
	adtyEdgeACMEDirectoryURL = func(bool) string { return acmeSrv.server.URL + "/directory" }
	adtyEdgeACMEHTTPClient = acmeSrv.server.Client()

	// Make authz initially pending on first fetch so DNS-01 TXT create + delete path executes.
	var cfMu sync.Mutex
	cfTXTs := map[string]AdtyCloudflareDNSRecord{}
	cfCreatedCount := 0
	cfDeletedCount := 0
	cfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfMu.Lock()
		defer cfMu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone-adtyaicf":
			writeJSON(w, map[string]any{"success": true, "result": map[string]string{"id": "zone-adtyaicf", "name": "adtyaicf.top"}})
		case r.Method == http.MethodPost && r.URL.Path == "/zones/zone-adtyaicf/dns_records":
			var body AdtyCloudflareDNSRecord
			_ = common.DecodeJson(r.Body, &body)
			cfCreatedCount++
			body.Id = fmt.Sprintf("txt-%d", cfCreatedCount)
			cfTXTs[body.Id] = body
			writeJSON(w, map[string]any{"success": true, "result": body})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/zones/zone-adtyaicf/dns_records/"):
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			delete(cfTXTs, id)
			cfDeletedCount++
			writeJSON(w, map[string]any{"success": true, "result": map[string]string{"id": id}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(cfSrv.Close)
	AdtyCloudflare = &AdtyCloudflareClient{
		BaseURL: cfSrv.URL,
		HTTP:    cfSrv.Client(),
		Token:   func() string { return "cf-test-token" },
	}

	// Patch mock ACME authz to require DNS-01 challenge (status=pending on first GET, valid after Accept).
	var authzAccepted sync.Map
	origHandler := acmeSrv.server.Config.Handler
	acmeSrv.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/chal/") {
			id := strings.TrimPrefix(r.URL.Path, "/chal/")
			authzAccepted.Store(id, true)
		}
		if strings.HasPrefix(r.URL.Path, "/authz/") {
			id := strings.TrimPrefix(r.URL.Path, "/authz/")
			if _, ok := authzAccepted.Load(id); !ok {
				w.Header().Set("Replay-Nonce", "test-nonce-12345")
				w.Header().Set("Content-Type", "application/json")
				acmeSrv.mu.Lock()
				dnsName := acmeSrv.orders[id]
				acmeSrv.mu.Unlock()
				writeJSON(w, map[string]any{
					"status":     "pending",
					"identifier": map[string]string{"type": "dns", "value": strings.TrimPrefix(dnsName, "*.")},
					"challenges": []map[string]any{
						{"type": "dns-01", "status": "pending", "url": acmeSrv.server.URL + "/chal/" + id, "token": "tok-" + id},
					},
				})
				return
			}
		}
		origHandler.ServeHTTP(w, r)
	})

	// Seed active site with 2 custom site domains (one CNAME ready, one waiting) + 1 closed redirect_only domain.
	site := model.AdtySite{OwnerUserId: 100, Subdomain: "s1", RootDomain: "adtyai-fz.top", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)
	closedSite := model.AdtySite{OwnerUserId: 101, Subdomain: "s2", RootDomain: "adtyai-fz.top", Status: model.AdtySiteStatusClosed}
	require.NoError(t, db.Create(&closedSite).Error)

	domReady := model.AdtySiteDomain{
		SiteId: site.Id, Hostname: "shop.owner.example", Role: model.AdtySiteDomainRoleSite,
		ManagedBy: "owner", DesiredState: "serve", Status: model.AdtySiteDomainActive, EdgeACMEAlias: "d10-aabbccdd",
	}
	domWaiting := model.AdtySiteDomain{
		SiteId: site.Id, Hostname: "wait.owner.example", Role: model.AdtySiteDomainRoleSite,
		ManagedBy: "owner", DesiredState: "serve", Status: model.AdtySiteDomainActive, EdgeACMEAlias: "d11-11223344",
	}
	domRedirect := model.AdtySiteDomain{
		SiteId: closedSite.Id, Hostname: "old.owner.example", Role: model.AdtySiteDomainRoleSite,
		ManagedBy: "owner", DesiredState: "redirect_only", Status: model.AdtySiteDomainActive, EdgeACMEAlias: "d12-55667788",
	}
	require.NoError(t, db.Create(&domReady).Error)
	require.NoError(t, db.Create(&domWaiting).Error)
	require.NoError(t, db.Create(&domRedirect).Error)

	cnameMap := map[string]string{}
	adtyEdgeLookupCNAME = func(_ context.Context, name string) (string, error) {
		if target, ok := cnameMap[strings.ToLower(name)]; ok {
			return target + ".", nil
		}
		return "", &net.DNSError{IsNotFound: true}
	}
	adtyEdgeLookupTXT = func(_ context.Context, name string) ([]string, error) {
		cfMu.Lock()
		defer cfMu.Unlock()
		var out []string
		for _, rec := range cfTXTs {
			if rec.Name == name {
				out = append(out, rec.Content)
			}
		}
		return out, nil
	}

	now := time.Now()
	// Run 1: No CNAME configured -> all waiting_dns, ZERO ACME orders and ZERO CF TXT writes.
	require.NoError(t, RunAdtyEdgeCertOnce(context.Background(), now))
	assert.Equal(t, 0, acmeSrv.orderCount)
	assert.Equal(t, 0, cfCreatedCount)

	// Configure CNAME for wildcard and shop.owner.example only.
	cnameMap["_acme-challenge.adtyai-fz.top"] = "wild.acme.adtyaicf.top"
	cnameMap["_acme-challenge.shop.owner.example"] = "d10-aabbccdd.acme.adtyaicf.top"

	// Run 2 (after 3m waiting retry): issues wildcard + shop.owner.example, cleans up all TXT records.
	require.NoError(t, RunAdtyEdgeCertOnce(context.Background(), now.Add(3*time.Minute)))
	assert.Equal(t, 2, acmeSrv.orderCount)
	assert.Equal(t, 2, cfCreatedCount)
	assert.Equal(t, 2, cfDeletedCount)
	assert.Empty(t, cfTXTs, "temporary DNS-01 TXT records must be cleaned up")

	var wcRow model.AdtyEdgeCert
	require.NoError(t, db.Where("cert_name = ?", "*.adtyai-fz.top").First(&wcRow).Error)
	assert.Equal(t, model.AdtyEdgeCertStatusReady, wcRow.Status)
	assert.True(t, strings.HasPrefix(wcRow.KeyEncrypted, "adty:secret:v1:"))
	plainKey, err := model.OpenAdtyPurposeSecret(adtyEdgeCertKeyPurpose(wcRow.CertName), wcRow.KeyEncrypted)
	require.NoError(t, err)
	assert.Contains(t, plainKey, "BEGIN EC PRIVATE KEY")

	var updatedReady model.AdtySiteDomain
	require.NoError(t, db.First(&updatedReady, domReady.Id).Error)
	assert.Equal(t, model.AdtyEdgeCertStatusReady, updatedReady.EdgeCertStatus)
	assert.Greater(t, updatedReady.EdgeCertNotAfter, now.Unix())

	// Manifest: wildcard ready, shop.owner.example -> terminate, wait.owner.example -> passthrough_cf, old.owner.example -> redirect_only.
	m1, err := BuildAdtyEdgeManifest(context.Background(), now.Add(4*time.Minute))
	require.NoError(t, err)
	require.Len(t, m1.WildcardCerts, 1)
	assert.Equal(t, "adtyai-fz.top", m1.WildcardCerts[0].RootDomain)
	require.Len(t, m1.CustomDomains, 3)

	byHost := map[string]AdtyEdgeManifestCustomDomain{}
	for _, d := range m1.CustomDomains {
		byHost[d.Hostname] = d
	}
	assert.Equal(t, "terminate", byHost["shop.owner.example"].Action)
	assert.NotEmpty(t, byHost["shop.owner.example"].CertPEM)
	assert.NotEmpty(t, byHost["shop.owner.example"].KeyPEM)
	assert.Equal(t, "passthrough_cf", byHost["wait.owner.example"].Action)
	assert.Empty(t, byHost["wait.owner.example"].CertPEM)
	assert.Equal(t, "redirect_only", byHost["old.owner.example"].Mode)
	assert.Equal(t, "passthrough_cf", byHost["old.owner.example"].Action)

	// Manifest version is deterministic across timestamps when content is unchanged.
	m2, err := BuildAdtyEdgeManifest(context.Background(), now.Add(5*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, m1.Version, m2.Version)

	// Renewal within 30 days: simulate failure first -> TXT still deleted, ready status preserved (>24h left).
	require.NoError(t, db.Model(&model.AdtyEdgeCert{}).Where("id = ?", wcRow.Id).Updates(map[string]any{
		"not_after":   now.Add(10 * 24 * time.Hour).Unix(),
		"next_run_at": 0,
	}).Error)
	acmeSrv.mu.Lock()
	acmeSrv.failFinalize = true
	acmeSrv.mu.Unlock()
	err = RunAdtyEdgeCertOnce(context.Background(), now.Add(10*time.Minute))
	require.Error(t, err)
	assert.Empty(t, cfTXTs, "TXT record must be deleted even when ACME finalize fails")
	require.NoError(t, db.First(&wcRow, wcRow.Id).Error)
	assert.Equal(t, model.AdtyEdgeCertStatusReady, wcRow.Status, "valid cert with >24h left must keep ready status on renewal error")
	assert.NotEmpty(t, wcRow.LastError)
	assert.Equal(t, 1, wcRow.Attempts)
	assert.Greater(t, wcRow.NextRunAt, now.Add(10*time.Minute).Unix())

	// Recovery renewal succeeds when finalize recovers and backoff expires.
	acmeSrv.mu.Lock()
	acmeSrv.failFinalize = false
	acmeSrv.mu.Unlock()
	require.NoError(t, RetryAdtyEdgeCert(""))
	require.NoError(t, RunAdtyEdgeCertOnce(context.Background(), now.Add(11*time.Minute)))
	require.NoError(t, db.First(&wcRow, wcRow.Id).Error)
	assert.Equal(t, model.AdtyEdgeCertStatusReady, wcRow.Status)
	assert.Empty(t, wcRow.LastError)
	assert.Equal(t, 0, wcRow.Attempts)
	assert.Greater(t, wcRow.NotAfter, now.Add(60*24*time.Hour).Unix())
}

func TestAdtyPhase4DNSSyncFailoverAndProbe(t *testing.T) {
	db := setupEdgeTestEnv(t, func(rs *adty_setting.ResellerSetting) {
		rs.EdgeNodeIP = "47.242.10.20"
		rs.EdgeProbeEnabled = true
	})

	var cfMu sync.Mutex
	records := map[string]AdtyCloudflareDNSRecord{}
	failCF := false
	cfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfMu.Lock()
		defer cfMu.Unlock()
		if failCF {
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, map[string]any{"success": false, "errors": []map[string]any{{"code": 500, "message": "boom"}}})
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone-adtyaicf":
			writeJSON(w, map[string]any{"success": true, "result": map[string]string{"id": "zone-adtyaicf", "name": "adtyaicf.top"}})
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone-adtyaicf/dns_records":
			name := r.URL.Query().Get("name")
			typ := r.URL.Query().Get("type")
			var out []AdtyCloudflareDNSRecord
			for _, rec := range records {
				if (name == "" || rec.Name == name) && (typ == "" || rec.Type == typ) {
					out = append(out, rec)
				}
			}
			writeJSON(w, map[string]any{"success": true, "result": out})
		case r.Method == http.MethodPost && r.URL.Path == "/zones/zone-adtyaicf/dns_records":
			var rec AdtyCloudflareDNSRecord
			_ = common.DecodeJson(r.Body, &rec)
			rec.Id = "a-rec-1"
			records[rec.Id] = rec
			writeJSON(w, map[string]any{"success": true, "result": rec})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/zones/zone-adtyaicf/dns_records/"):
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			var rec AdtyCloudflareDNSRecord
			_ = common.DecodeJson(r.Body, &rec)
			rec.Id = id
			records[id] = rec
			writeJSON(w, map[string]any{"success": true, "result": rec})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(cfSrv.Close)
	AdtyCloudflare = &AdtyCloudflareClient{
		BaseURL: cfSrv.URL,
		HTTP:    cfSrv.Client(),
		Token:   func() string { return "cf-test-token" },
	}

	// Refuse syncing if domain_mainland_target collides with cname_target.
	rsPtr := config.GlobalConfig.Get(adty_setting.ResellerSettingModule).(*adty_setting.ResellerSetting)
	common.OptionMapRWMutex.Lock()
	rsPtr.DomainMainlandTarget = rsPtr.CnameTarget
	common.OptionMapRWMutex.Unlock()
	_, err := SyncAdtyEdgeMainlandDNS(context.Background())
	require.Error(t, err)
	common.OptionMapRWMutex.Lock()
	rsPtr.DomainMainlandTarget = "cn.adtyaicf.top"
	common.OptionMapRWMutex.Unlock()

	// Normal sync creates grey-cloud A record with TTL 60.
	state, err := SyncAdtyEdgeMainlandDNS(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "cn.adtyaicf.top", state.Target)
	assert.Equal(t, "47.242.10.20", state.IP)
	assert.False(t, state.Proxied)
	assert.Equal(t, 60, state.TTL)

	// Failover to CF updates A record to proxied=true, TTL=1 and persists setting.
	state, err = SetAdtyEdgeFailover(context.Background(), true)
	require.NoError(t, err)
	assert.True(t, state.Proxied)
	assert.Equal(t, 1, state.TTL)
	assert.True(t, adty_setting.GetResellerSetting().EdgeFailoverToCF)

	// CF error prevents flipping stored failover flag.
	cfMu.Lock()
	failCF = true
	cfMu.Unlock()
	_, err = SetAdtyEdgeFailover(context.Background(), false)
	require.Error(t, err)
	assert.True(t, adty_setting.GetResellerSetting().EdgeFailoverToCF)

	// Switch back succeeds once CF recovers.
	cfMu.Lock()
	failCF = false
	cfMu.Unlock()
	state, err = SetAdtyEdgeFailover(context.Background(), false)
	require.NoError(t, err)
	assert.False(t, state.Proxied)
	assert.Equal(t, 60, state.TTL)
	assert.False(t, adty_setting.GetResellerSetting().EdgeFailoverToCF)

	// Origin-to-edge HTTPS health probe test against a local TLS server with matching cert.
	site := model.AdtySite{OwnerUserId: 200, Subdomain: "alpha", RootDomain: "adtyai-fz.top", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)
	dom := model.AdtySiteDomain{
		SiteId: site.Id, Hostname: "alpha.adtyai-fz.top", Role: model.AdtySiteDomainRoleSite,
		ManagedBy: "platform", DesiredState: "serve", Status: model.AdtySiteDomainActive,
	}
	require.NoError(t, db.Create(&dom).Error)

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(10), Subject: pkix.Name{CommonName: "Edge Probe Test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(11), Subject: pkix.Name{CommonName: "*.adtyai-fz.top"},
		DNSNames: []string{"*.adtyai-fz.top"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	require.NoError(t, err)

	probeStatusCode := http.StatusOK
	edgeSrv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if probeStatusCode != http.StatusOK {
			w.WriteHeader(probeStatusCode)
			return
		}
		writeJSON(w, AdtyDomainProbeIdentity{
			SiteID:   site.Id,
			Role:     model.AdtySiteDomainRoleSite,
			Hostname: "alpha.adtyai-fz.top",
		})
	}))
	edgeSrv.TLS = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}},
	}
	edgeSrv.StartTLS()
	t.Cleanup(edgeSrv.Close)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	adtyEdgeProbeRootCAs = pool
	adtyEdgeProbeDialAddr = func(string) string { return edgeSrv.Listener.Addr().String() }

	probeState, err := RunAdtyEdgeProbeOnce(context.Background(), time.Now(), false)
	require.NoError(t, err)
	require.NotNil(t, probeState)
	assert.True(t, probeState.Healthy)
	assert.Equal(t, http.StatusOK, probeState.HTTPStatus)
	assert.Equal(t, "alpha.adtyai-fz.top", probeState.ProbeHost)
	assert.Equal(t, 1, probeState.ConsecutiveSuccesses)
	assert.Greater(t, probeState.CertNotAfter, time.Now().Unix())

	probeStatusCode = http.StatusBadGateway
	probeState, err = RunAdtyEdgeProbeOnce(context.Background(), time.Now().Add(time.Minute), false)
	require.NoError(t, err)
	assert.False(t, probeState.Healthy)
	assert.Equal(t, http.StatusBadGateway, probeState.HTTPStatus)
	assert.Equal(t, 1, probeState.ConsecutiveFailures)
	assert.NotEmpty(t, probeState.LastError)
}

func TestAdtyMultiNodeProbeAutoEjectAndRecovery(t *testing.T) {
	ip1 := "47.242.10.20"
	ip2 := "47.242.10.21"
	db := setupEdgeTestEnv(t, func(rs *adty_setting.ResellerSetting) {
		rs.EdgeNodeIP = ip1
		rs.EdgeNodeIPs = []string{ip1, ip2}
		rs.EdgeProbeEnabled = true
		rs.EdgeAutoFailover = true
	})

	var cfMu sync.Mutex
	nextID := 0
	records := map[string]AdtyCloudflareDNSRecord{}
	cfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfMu.Lock()
		defer cfMu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone-adtyaicf":
			writeJSON(w, map[string]any{"success": true, "result": map[string]string{"id": "zone-adtyaicf", "name": "adtyaicf.top"}})
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone-adtyaicf/dns_records":
			name := r.URL.Query().Get("name")
			typ := r.URL.Query().Get("type")
			var out []AdtyCloudflareDNSRecord
			for _, rec := range records {
				if (name == "" || rec.Name == name) && (typ == "" || rec.Type == typ) {
					out = append(out, rec)
				}
			}
			writeJSON(w, map[string]any{"success": true, "result": out})
		case r.Method == http.MethodPost && r.URL.Path == "/zones/zone-adtyaicf/dns_records":
			var rec AdtyCloudflareDNSRecord
			_ = common.DecodeJson(r.Body, &rec)
			nextID++
			rec.Id = fmt.Sprintf("a-rec-%d", nextID)
			records[rec.Id] = rec
			writeJSON(w, map[string]any{"success": true, "result": rec})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/zones/zone-adtyaicf/dns_records/"):
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			var rec AdtyCloudflareDNSRecord
			_ = common.DecodeJson(r.Body, &rec)
			rec.Id = id
			records[id] = rec
			writeJSON(w, map[string]any{"success": true, "result": rec})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/zones/zone-adtyaicf/dns_records/"):
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			delete(records, id)
			writeJSON(w, map[string]any{"success": true, "result": map[string]string{"id": id}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(cfSrv.Close)
	AdtyCloudflare = &AdtyCloudflareClient{
		BaseURL: cfSrv.URL,
		HTTP:    cfSrv.Client(),
		Token:   func() string { return "cf-test-token" },
	}

	cfSnapshot := func() (ips []string, proxied bool) {
		cfMu.Lock()
		defer cfMu.Unlock()
		for _, rec := range records {
			if rec.Type == "A" && rec.Name == "cn.adtyaicf.top" {
				ips = append(ips, rec.Content)
				proxied = rec.Proxied
			}
		}
		return ips, proxied
	}

	// Sync initial 2-node grey-cloud round-robin A records.
	syncState, err := SyncAdtyEdgeMainlandDNS(context.Background())
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{ip1, ip2}, syncState.IPs)
	assert.False(t, syncState.Proxied)
	liveIPs, liveProxied := cfSnapshot()
	assert.ElementsMatch(t, []string{ip1, ip2}, liveIPs)
	assert.False(t, liveProxied)

	// Setup site + two local TLS servers simulating HK VPS-1 and HK VPS-2.
	site := model.AdtySite{OwnerUserId: 210, Subdomain: "beta", RootDomain: "adtyai-fz.top", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)
	dom := model.AdtySiteDomain{
		SiteId: site.Id, Hostname: "beta.adtyai-fz.top", Role: model.AdtySiteDomainRoleSite,
		ManagedBy: "platform", DesiredState: "serve", Status: model.AdtySiteDomainActive,
	}
	require.NoError(t, db.Create(&dom).Error)

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(20), Subject: pkix.Name{CommonName: "Multi Edge Test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(21), Subject: pkix.Name{CommonName: "*.adtyai-fz.top"},
		DNSNames: []string{"*.adtyai-fz.top"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	require.NoError(t, err)

	var statusMu sync.Mutex
	nodeStatus := map[string]int{ip1: http.StatusOK, ip2: http.StatusOK}
	makeEdgeServer := func(ip string) *httptest.Server {
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			statusMu.Lock()
			code := nodeStatus[ip]
			statusMu.Unlock()
			if code != http.StatusOK {
				w.WriteHeader(code)
				return
			}
			writeJSON(w, AdtyDomainProbeIdentity{
				SiteID:   site.Id,
				Role:     model.AdtySiteDomainRoleSite,
				Hostname: "beta.adtyai-fz.top",
			})
		}))
		srv.TLS = &tls.Config{
			Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}},
		}
		srv.StartTLS()
		t.Cleanup(srv.Close)
		return srv
	}
	srv1 := makeEdgeServer(ip1)
	srv2 := makeEdgeServer(ip2)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	adtyEdgeProbeRootCAs = pool
	adtyEdgeProbeDialAddr = func(nodeIP string) string {
		if nodeIP == ip2 {
			return srv2.Listener.Addr().String()
		}
		return srv1.Listener.Addr().String()
	}

	t0 := time.Now()
	// 1. Both healthy -> 2/2 healthy, both in DNS.
	p, err := RunAdtyEdgeProbeOnce(context.Background(), t0, false)
	require.NoError(t, err)
	assert.True(t, p.Healthy)
	assert.Equal(t, 2, p.HealthyCount)
	assert.Equal(t, 2, p.TotalCount)
	require.Len(t, p.Nodes, 2)
	assert.False(t, p.Nodes[0].Ejected)
	assert.False(t, p.Nodes[1].Ejected)

	// 2. Node 2 fails twice -> anti-flapping keeps both IPs in DNS.
	statusMu.Lock()
	nodeStatus[ip2] = http.StatusBadGateway
	statusMu.Unlock()
	for step := 1; step <= 2; step++ {
		p, err = RunAdtyEdgeProbeOnce(context.Background(), t0.Add(time.Duration(step)*time.Minute), false)
		require.NoError(t, err)
		assert.Equal(t, 1, p.HealthyCount)
		assert.Equal(t, step, p.Nodes[1].ConsecutiveFailures)
		assert.False(t, p.Nodes[1].Ejected)
		liveIPs, liveProxied = cfSnapshot()
		assert.ElementsMatch(t, []string{ip1, ip2}, liveIPs)
		assert.False(t, liveProxied)
	}

	// 3. Node 2 fails 3rd consecutive time -> ejected from Cloudflare DNS automatically.
	p, err = RunAdtyEdgeProbeOnce(context.Background(), t0.Add(3*time.Minute), false)
	require.NoError(t, err)
	assert.True(t, p.Nodes[1].Ejected)
	assert.False(t, p.AutoFailoverActive)
	assert.Equal(t, "ejected_unhealthy", p.LastAutoAction)
	assert.Equal(t, []string{ip1}, p.ActiveDNSIPs)
	liveIPs, liveProxied = cfSnapshot()
	assert.Equal(t, []string{ip1}, liveIPs)
	assert.False(t, liveProxied)

	// 4. Node 1 also fails 3 times -> all nodes ejected -> auto-failover to Cloudflare orange cloud!
	statusMu.Lock()
	nodeStatus[ip1] = http.StatusServiceUnavailable
	statusMu.Unlock()
	for step := 4; step <= 6; step++ {
		p, err = RunAdtyEdgeProbeOnce(context.Background(), t0.Add(time.Duration(step)*time.Minute), false)
		require.NoError(t, err)
	}
	assert.True(t, p.Nodes[0].Ejected)
	assert.True(t, p.Nodes[1].Ejected)
	assert.True(t, p.AutoFailoverActive)
	assert.Equal(t, "auto_failover_cf", p.LastAutoAction)
	assert.False(t, adty_setting.GetResellerSetting().EdgeFailoverToCF, "manual failover switch must stay unchanged")
	liveIPs, liveProxied = cfSnapshot()
	assert.Len(t, liveIPs, 1)
	assert.True(t, liveProxied)

	// 5. Node 2 recovers once (still ejected), recovers 2nd time -> restores ip2 to grey-cloud DNS while ip1 stays ejected.
	statusMu.Lock()
	nodeStatus[ip2] = http.StatusOK
	statusMu.Unlock()
	p, err = RunAdtyEdgeProbeOnce(context.Background(), t0.Add(7*time.Minute), false)
	require.NoError(t, err)
	assert.True(t, p.Nodes[1].Ejected)
	assert.True(t, p.AutoFailoverActive)

	p, err = RunAdtyEdgeProbeOnce(context.Background(), t0.Add(8*time.Minute), false)
	require.NoError(t, err)
	assert.False(t, p.Nodes[1].Ejected)
	assert.True(t, p.Nodes[0].Ejected)
	assert.False(t, p.AutoFailoverActive)
	assert.Equal(t, []string{ip2}, p.ActiveDNSIPs)
	liveIPs, liveProxied = cfSnapshot()
	assert.Equal(t, []string{ip2}, liveIPs)
	assert.False(t, liveProxied)

	// 6. Manual failover locks Cloudflare orange cloud even while nodes are healthy; switching back restores both IPs.
	statusMu.Lock()
	nodeStatus[ip1] = http.StatusOK
	statusMu.Unlock()
	_, err = SetAdtyEdgeFailover(context.Background(), true)
	require.NoError(t, err)
	p, err = RunAdtyEdgeProbeOnce(context.Background(), t0.Add(9*time.Minute), false)
	require.NoError(t, err)
	assert.Equal(t, 2, p.HealthyCount)
	_, liveProxied = cfSnapshot()
	assert.True(t, liveProxied, "manual Cloudflare failover must not be undone by probe")

	_, err = SetAdtyEdgeFailover(context.Background(), false)
	require.NoError(t, err)
	liveIPs, liveProxied = cfSnapshot()
	assert.ElementsMatch(t, []string{ip1, ip2}, liveIPs)
	assert.False(t, liveProxied)
}

func TestAdtyEdgeMainSiteCertManifestAndValidation(t *testing.T) {
	// Settings validation for main site edge fields.
	cfg := adty_setting.DefaultResellerSetting()
	cfg.EdgeMainSiteEnabled = true
	cfg.EdgeMainSiteHost = ""
	require.Error(t, adty_setting.ValidateResellerPatch(&cfg, false, map[string]string{"edge_main_site_enabled": "true"}))

	cfg.EdgeMainSiteHost = "not a host"
	require.Error(t, adty_setting.ValidateResellerPatch(&cfg, false, map[string]string{"edge_main_site_host": "not a host"}))

	cfg.EdgeMainSiteHost = cfg.DomainMainlandTarget
	require.Error(t, adty_setting.ValidateResellerPatch(&cfg, false, map[string]string{"edge_main_site_enabled": "true", "edge_main_site_host": cfg.DomainMainlandTarget}))

	cfg.EdgeMainSiteHost = "www.adtyai.com"
	cfg.EdgeMainSiteOriginSNI = "bad host"
	require.Error(t, adty_setting.ValidateResellerPatch(&cfg, false, map[string]string{"edge_main_site_origin_sni": "bad host"}))

	cfg.EdgeMainSiteOriginSNI = "www.adtyai.com"
	require.NoError(t, adty_setting.ValidateResellerPatch(&cfg, false, map[string]string{
		"edge_main_site_enabled":    "true",
		"edge_main_site_host":       "www.adtyai.com",
		"edge_main_site_origin_sni": "www.adtyai.com",
	}))

	// End-to-end ACME issuance + manifest + status for main site website domain.
	db := setupEdgeTestEnv(t, func(rs *adty_setting.ResellerSetting) {
		rs.EdgeMainSiteEnabled = true
		rs.EdgeMainSiteHost = "www.adtyai.com"
		rs.EdgeMainSiteOriginSNI = "www.adtyai.com"
	})
	acmeSrv := newMockACMEServer(t)
	adtyEdgeACMEDirectoryURL = func(bool) string { return acmeSrv.server.URL + "/directory" }
	adtyEdgeACMEHTTPClient = acmeSrv.server.Client()

	var cfMu sync.Mutex
	cfTXTs := map[string]AdtyCloudflareDNSRecord{}
	cfCreatedCount := 0
	cfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfMu.Lock()
		defer cfMu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone-adtyaicf":
			writeJSON(w, map[string]any{"success": true, "result": map[string]string{"id": "zone-adtyaicf", "name": "adtyaicf.top"}})
		case r.Method == http.MethodPost && r.URL.Path == "/zones/zone-adtyaicf/dns_records":
			var body AdtyCloudflareDNSRecord
			_ = common.DecodeJson(r.Body, &body)
			cfCreatedCount++
			body.Id = fmt.Sprintf("txt-%d", cfCreatedCount)
			cfTXTs[body.Id] = body
			writeJSON(w, map[string]any{"success": true, "result": body})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/zones/zone-adtyaicf/dns_records/"):
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			delete(cfTXTs, id)
			writeJSON(w, map[string]any{"success": true, "result": map[string]string{"id": id}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(cfSrv.Close)
	AdtyCloudflare = &AdtyCloudflareClient{
		BaseURL: cfSrv.URL,
		HTTP:    cfSrv.Client(),
		Token:   func() string { return "cf-test-token" },
	}

	cnameMap := map[string]string{}
	adtyEdgeLookupCNAME = func(_ context.Context, name string) (string, error) {
		if target, ok := cnameMap[strings.ToLower(name)]; ok {
			return target + ".", nil
		}
		return "", &net.DNSError{IsNotFound: true}
	}
	adtyEdgeLookupTXT = func(_ context.Context, name string) ([]string, error) {
		cfMu.Lock()
		defer cfMu.Unlock()
		var out []string
		for _, rec := range cfTXTs {
			if rec.Name == name {
				out = append(out, rec.Content)
			}
		}
		return out, nil
	}

	now := time.Now()
	// Before CNAME is set, manifest includes www.adtyai.com as passthrough_cf with custom origin_sni.
	require.NoError(t, RunAdtyEdgeCertOnce(context.Background(), now))
	mBefore, err := BuildAdtyEdgeManifest(context.Background(), now)
	require.NoError(t, err)
	require.Len(t, mBefore.CustomDomains, 1)
	assert.Equal(t, "www.adtyai.com", mBefore.CustomDomains[0].Hostname)
	assert.Equal(t, "passthrough_cf", mBefore.CustomDomains[0].Action)
	assert.Equal(t, "www.adtyai.com", mBefore.CustomDomains[0].OriginSNI)

	statusBefore, err := GetAdtyEdgeStatus()
	require.NoError(t, err)
	require.NotNil(t, statusBefore.MainSiteCert)
	assert.Equal(t, "www.adtyai.com", statusBefore.MainSiteCert.CertName)
	assert.Equal(t, "_acme-challenge.www.adtyai.com", statusBefore.MainSiteCert.ChallengeHost)
	assert.Equal(t, "main.acme.adtyaicf.top", statusBefore.MainSiteCert.AliasFQDN)
	assert.Equal(t, model.AdtyEdgeCertStatusWaitingDNS, statusBefore.MainSiteCert.Status)

	// Add _acme-challenge.www.adtyai.com -> main.acme.adtyaicf.top and trigger retry + issue.
	cnameMap["_acme-challenge.www.adtyai.com"] = "main.acme.adtyaicf.top"
	require.NoError(t, RetryAdtyEdgeCert("www.adtyai.com"))
	require.NoError(t, RunAdtyEdgeCertOnce(context.Background(), now.Add(3*time.Minute)))

	var mainRow model.AdtyEdgeCert
	require.NoError(t, db.Where("cert_name = ?", "www.adtyai.com").First(&mainRow).Error)
	assert.Equal(t, model.AdtyEdgeCertKindMain, mainRow.Kind)
	assert.Equal(t, model.AdtyEdgeCertStatusReady, mainRow.Status)

	mAfter, err := BuildAdtyEdgeManifest(context.Background(), now.Add(4*time.Minute))
	require.NoError(t, err)
	require.Len(t, mAfter.CustomDomains, 1)
	assert.Equal(t, "www.adtyai.com", mAfter.CustomDomains[0].Hostname)
	assert.Equal(t, "terminate", mAfter.CustomDomains[0].Action)
	assert.Equal(t, "www.adtyai.com", mAfter.CustomDomains[0].OriginSNI)
	assert.NotEmpty(t, mAfter.CustomDomains[0].CertPEM)
	assert.NotEmpty(t, mAfter.CustomDomains[0].KeyPEM)

	statusAfter, err := GetAdtyEdgeStatus()
	require.NoError(t, err)
	require.NotNil(t, statusAfter.MainSiteCert)
	assert.Equal(t, model.AdtyEdgeCertStatusReady, statusAfter.MainSiteCert.Status)

	// Disabling edge_main_site_enabled removes main site from manifest and status summary.
	rsPtr := config.GlobalConfig.Get(adty_setting.ResellerSettingModule).(*adty_setting.ResellerSetting)
	common.OptionMapRWMutex.Lock()
	rsPtr.EdgeMainSiteEnabled = false
	common.OptionMapRWMutex.Unlock()
	mDisabled, err := BuildAdtyEdgeManifest(context.Background(), now.Add(5*time.Minute))
	require.NoError(t, err)
	assert.Empty(t, mDisabled.CustomDomains)
	statusDisabled, err := GetAdtyEdgeStatus()
	require.NoError(t, err)
	assert.Nil(t, statusDisabled.MainSiteCert)
}

func TestEdgeCertParallelPendingIssuanceAndOnDemandOwnerCheck(t *testing.T) {
	db := setupEdgeTestEnv(t, func(rs *adty_setting.ResellerSetting) {
		rs.EdgeMainlandEnabled = true
		rs.EdgeACMEAliasSuffix = "acme.adtyaicf.top"
		rs.DomainMainlandTarget = "cn.adtyaicf.top"
		rs.RootDomains = []string{"adtyai-fz.top"}
	})
	acmeSrv := newMockACMEServer(t)
	adtyEdgeACMEDirectoryURL = func(bool) string { return acmeSrv.server.URL + "/directory" }
	adtyEdgeACMEHTTPClient = acmeSrv.server.Client()

	var cfMu sync.Mutex
	cfTXTs := map[string]AdtyCloudflareDNSRecord{}
	cfSeq := 0
	cfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfMu.Lock()
		defer cfMu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone-adtyaicf":
			writeJSON(w, map[string]any{"success": true, "result": map[string]string{"id": "zone-adtyaicf", "name": "adtyaicf.top"}})
		case r.Method == http.MethodPost && r.URL.Path == "/zones/zone-adtyaicf/dns_records":
			var body AdtyCloudflareDNSRecord
			_ = common.DecodeJson(r.Body, &body)
			cfSeq++
			body.Id = fmt.Sprintf("txt-p-%d", cfSeq)
			cfTXTs[body.Id] = body
			writeJSON(w, map[string]any{"success": true, "result": body})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/zones/zone-adtyaicf/dns_records/"):
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			delete(cfTXTs, id)
			writeJSON(w, map[string]any{"success": true, "result": map[string]string{"id": id}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(cfSrv.Close)

	oldCooldown := adtyOwnerDomainCheckCooldown
	t.Cleanup(func() {
		adtyOwnerDomainCheckCooldown = oldCooldown
	})
	AdtyCloudflare = &AdtyCloudflareClient{BaseURL: cfSrv.URL, HTTP: cfSrv.Client(), Token: func() string { return "tok" }}
	adtyOwnerDomainCheckCooldown = 10 * time.Second

	site := model.AdtySite{OwnerUserId: 801, Subdomain: "par", RootDomain: "adtyai-fz.top", Status: model.AdtySiteStatusActive}
	require.NoError(t, db.Create(&site).Error)

	pendingDom := model.AdtySiteDomain{
		SiteId: site.Id, Hostname: "www.parallel.example", Role: model.AdtySiteDomainRoleSite,
		ManagedBy: "owner", DesiredState: "serve", Status: model.AdtySiteDomainPending, EdgeACMEAlias: "d90-aabbccdd",
	}
	activeDom := model.AdtySiteDomain{
		SiteId: site.Id, Hostname: "www.ondemand.example", Role: model.AdtySiteDomainRoleSite,
		ManagedBy: "owner", DesiredState: "serve", Status: model.AdtySiteDomainActive, EdgeACMEAlias: "d91-11223344",
		LastCheckedAt: time.Now().Add(-time.Minute).Unix(),
	}
	require.NoError(t, db.Create(&pendingDom).Error)
	require.NoError(t, db.Create(&activeDom).Error)

	cnameMap := map[string]string{
		"_acme-challenge.www.parallel.example": "d90-aabbccdd.acme.adtyaicf.top",
	}
	adtyEdgeLookupCNAME = func(_ context.Context, name string) (string, error) {
		if target, ok := cnameMap[strings.ToLower(name)]; ok {
			return target + ".", nil
		}
		return "", &net.DNSError{IsNotFound: true}
	}
	adtyEdgeLookupTXT = func(_ context.Context, name string) ([]string, error) {
		cfMu.Lock()
		defer cfMu.Unlock()
		var out []string
		for _, rec := range cfTXTs {
			if rec.Name == name {
				out = append(out, rec.Content)
			}
		}
		return out, nil
	}

	// 1. Background RunAdtyEdgeCertOnce pre-issues cert for pending custom site domain in parallel,
	//    while BuildAdtyEdgeManifest still keeps the pending domain excluded until status=active.
	now := time.Now()
	require.NoError(t, RunAdtyEdgeCertOnce(context.Background(), now))
	var refreshedPending model.AdtySiteDomain
	require.NoError(t, db.First(&refreshedPending, pendingDom.Id).Error)
	assert.Equal(t, model.AdtyEdgeCertStatusReady, refreshedPending.EdgeCertStatus)

	mPending, err := BuildAdtyEdgeManifest(context.Background(), now.Add(time.Minute))
	require.NoError(t, err)
	for _, entry := range mPending.CustomDomains {
		assert.NotEqual(t, "www.parallel.example", entry.Hostname, "pending domain must not enter edge manifest before activation")
	}

	// 2. Owner clicks "Check certificate" on an already-active site domain via CheckAdtySiteDomainNow.
	cnameMap["_acme-challenge.www.ondemand.example"] = "d91-11223344.acme.adtyaicf.top"
	checkedActive, err := CheckAdtySiteDomainNow(context.Background(), 801, activeDom.Id)
	require.NoError(t, err)
	assert.Equal(t, model.AdtyEdgeCertStatusReady, checkedActive.EdgeCertStatus)
	assert.Empty(t, checkedActive.EdgeCertError)
}
