package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
)

type stubHostedOrderQuerier struct {
	queryFn func(ctx context.Context, ext *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error)
}

func (s stubHostedOrderQuerier) Query(ctx context.Context, ext *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error) {
	return s.queryFn(ctx, ext)
}

type unverifiedCustomRoundTripper struct {
	called bool
}

func (u *unverifiedCustomRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	u.called = true
	return nil, errors.New("unexpected round trip on unverified custom transport")
}

func ensureTestRelayHTTPClient() *http.Client {
	if GetHttpClient() == nil {
		InitHttpClient()
	}
	return GetHttpClient()
}

func TestNewAdtyEpayVerifiedHTTPClientForcesVerifiedTLS(t *testing.T) {
	t.Setenv("TLS_INSECURE_SKIP_VERIFY", "true")

	base := ensureTestRelayHTTPClient()
	prevTransport := base.Transport
	t.Cleanup(func() {
		base.Transport = prevTransport
	})

	insecureRelayTransport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS10,
		},
	}
	base.Transport = insecureRelayTransport

	verifiedClient, err := NewAdtyEpayVerifiedHTTPClient(5 * time.Second)
	if err != nil {
		t.Fatalf("NewAdtyEpayVerifiedHTTPClient returned unexpected error: %v", err)
	}
	verifiedTransport, ok := verifiedClient.Transport.(*http.Transport)
	if !ok || verifiedTransport == nil || verifiedTransport.TLSClientConfig == nil {
		t.Fatalf("expected *http.Transport with non-nil TLSClientConfig, got %T", verifiedClient.Transport)
	}
	if verifiedTransport.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("expected verified Epay transport InsecureSkipVerify=false, got true")
	}
	if verifiedTransport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Fatalf("expected verified Epay transport MinVersion >= TLS1.2, got %#x", verifiedTransport.TLSClientConfig.MinVersion)
	}
	if !insecureRelayTransport.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("expected shared relay transport to remain untouched (InsecureSkipVerify=true)")
	}
}

func TestRecoverAdtyHostedPaymentRejectsUntrustedCertificateWhenRelaySkipVerifyEnabled(t *testing.T) {
	t.Setenv("TLS_INSECURE_SKIP_VERIFY", "true")

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"status":1,"out_trade_no":"T-EPAY-MITM","trade_no":"GW-1","money":"10.00"}`))
	}))
	defer srv.Close()

	base := ensureTestRelayHTTPClient()
	prevTransport := base.Transport
	prevProviderLookup := adtyHostedRecoveryProviderByName
	prevRecordAttempt := recordAdtyHostedPaymentAttempt
	t.Cleanup(func() {
		base.Transport = prevTransport
		adtyHostedRecoveryProviderByName = prevProviderLookup
		recordAdtyHostedPaymentAttempt = prevRecordAttempt
	})

	base.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	var recordedStatus string
	recordAdtyHostedPaymentAttempt = func(tradeNo, provider, source, status string, operatorID int) error {
		recordedStatus = status
		return nil
	}

	var observedSkipVerifyDuringQuery *bool
	adtyHostedRecoveryProviderByName = func(name string) any {
		return stubHostedOrderQuerier{
			queryFn: func(ctx context.Context, ext *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error) {
				client := *GetHttpClient()
				if tr, ok := client.Transport.(*http.Transport); ok && tr != nil && tr.TLSClientConfig != nil {
					v := tr.TLSClientConfig.InsecureSkipVerify
					observedSkipVerifyDuringQuery = &v
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api.php?act=order", nil)
				if err != nil {
					return nil, err
				}
				resp, err := client.Do(req)
				if err != nil {
					return nil, err
				}
				_ = resp.Body.Close()
				return &model.AdtyPaymentFacts{GatewayTradeId: "GW-1"}, nil
			},
		}
	}

	err := RecoverAdtyHostedPaymentAs(context.Background(), &model.AdtyTopupOrder{
		TradeNo:  "T-EPAY-MITM",
		Provider: "epay",
	}, 1)
	if err == nil {
		t.Fatalf("expected RecoverAdtyHostedPaymentAs to reject untrusted TLS certificate even when relay InsecureSkipVerify=true")
	}
	if observedSkipVerifyDuringQuery == nil || *observedSkipVerifyDuringQuery {
		t.Fatalf("expected active transport during Epay recovery query to have InsecureSkipVerify=false, got %v", observedSkipVerifyDuringQuery)
	}
	if recordedStatus != "failed" {
		t.Fatalf("expected failed payment attempt to be recorded, got %q", recordedStatus)
	}
	if tr, ok := base.Transport.(*http.Transport); !ok || tr == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("expected shared relay transport to be restored after query")
	}
}

func TestRecoverAdtyHostedPaymentAllowsTrustedCertificateWithSkipVerifyForcedFalse(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"status":1}`))
	}))
	defer srv.Close()

	rootPool := x509.NewCertPool()
	rootPool.AddCert(srv.Certificate())

	base := ensureTestRelayHTTPClient()
	prevTransport := base.Transport
	t.Cleanup(func() {
		base.Transport = prevTransport
	})

	base.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:            rootPool,
			InsecureSkipVerify: true,
		},
	}

	var observedSkipVerify bool
	facts, err := queryAdtyHostedPaymentFacts(context.Background(), stubHostedOrderQuerier{
		queryFn: func(ctx context.Context, ext *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error) {
			client := *GetHttpClient()
			tr := client.Transport.(*http.Transport)
			observedSkipVerify = tr.TLSClientConfig.InsecureSkipVerify
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api.php?act=order", nil)
			if err != nil {
				return nil, err
			}
			resp, err := client.Do(req)
			if err != nil {
				return nil, err
			}
			_ = resp.Body.Close()
			return &model.AdtyPaymentFacts{GatewayTradeId: "GW-TRUSTED"}, nil
		},
	}, &model.AdtyTopupOrder{TradeNo: "T-EPAY-OK", Provider: "epay"})
	if err != nil {
		t.Fatalf("expected trusted TLS server to succeed with verified transport, got error: %v", err)
	}
	if observedSkipVerify {
		t.Fatalf("expected InsecureSkipVerify=false during trusted Epay query, got true")
	}
	if facts == nil || facts.GatewayTradeId != "GW-TRUSTED" {
		t.Fatalf("unexpected facts: %+v", facts)
	}
}

func TestRecoverAdtyHostedPaymentFailsClosedOnUnverifiedCustomRoundTripper(t *testing.T) {
	base := ensureTestRelayHTTPClient()
	prevTransport := base.Transport
	prevProviderLookup := adtyHostedRecoveryProviderByName
	prevRecordAttempt := recordAdtyHostedPaymentAttempt
	t.Cleanup(func() {
		base.Transport = prevTransport
		adtyHostedRecoveryProviderByName = prevProviderLookup
		recordAdtyHostedPaymentAttempt = prevRecordAttempt
	})

	customRT := &unverifiedCustomRoundTripper{}
	base.Transport = customRT
	recordAdtyHostedPaymentAttempt = func(tradeNo, provider, source, status string, operatorID int) error {
		return nil
	}
	adtyHostedRecoveryProviderByName = func(name string) any {
		return stubHostedOrderQuerier{
			queryFn: func(ctx context.Context, ext *model.AdtyTopupOrder) (*model.AdtyPaymentFacts, error) {
				t.Fatalf("provider.Query must not be invoked when verified TLS transport cannot be guaranteed")
				return nil, nil
			},
		}
	}

	err := RecoverAdtyHostedPaymentAs(context.Background(), &model.AdtyTopupOrder{
		TradeNo:  "T-EPAY-CUSTOM-RT",
		Provider: "epay",
	}, 1)
	if !errors.Is(err, ErrAdtyUnverifiedPaymentTLS) {
		t.Fatalf("expected ErrAdtyUnverifiedPaymentTLS, got %v", err)
	}
	if customRT.called {
		t.Fatalf("expected custom unverified RoundTripper not to be called")
	}
}
