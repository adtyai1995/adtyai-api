package service

import (
	"crypto/tls"
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

var ErrAdtyUnverifiedPaymentTLS = errors.New("payment order query requires a verified TLS transport")

func GetCallbackAddress() string {
	addr := operation_setting.GetCustomCallbackAddress()
	if addr == "" {
		return system_setting.GetServerAddress()
	}
	return addr
}

func cloneAdtyVerifiedPaymentTransport(rt http.RoundTripper) (*http.Transport, error) {
	if rt == nil {
		rt = http.DefaultTransport
	}
	tr, ok := rt.(*http.Transport)
	if !ok || tr == nil {
		return nil, ErrAdtyUnverifiedPaymentTLS
	}
	cloned := tr.Clone()
	if cloned.TLSClientConfig == nil {
		cloned.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		cloned.TLSClientConfig = cloned.TLSClientConfig.Clone()
	}
	cloned.TLSClientConfig.InsecureSkipVerify = false
	if cloned.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		cloned.TLSClientConfig.MinVersion = tls.VersionTLS12
	}
	prevVerifyConnection := cloned.TLSClientConfig.VerifyConnection
	cloned.TLSClientConfig.VerifyConnection = func(cs tls.ConnectionState) error {
		if cs.Version < tls.VersionTLS12 {
			return ErrAdtyUnverifiedPaymentTLS
		}
		if len(cs.PeerCertificates) == 0 || len(cs.VerifiedChains) == 0 {
			return ErrAdtyUnverifiedPaymentTLS
		}
		if prevVerifyConnection != nil {
			return prevVerifyConnection(cs)
		}
		return nil
	}
	return cloned, nil
}

// NewAdtyEpayVerifiedHTTPClient constructs a dedicated HTTP client for Epay and
// hosted payment reconciliation that always enforces certificate verification
// (InsecureSkipVerify=false, TLS >= 1.2) regardless of relay TLS settings, and
// fails closed when verified TLS cannot be guaranteed.
func NewAdtyEpayVerifiedHTTPClient(timeout time.Duration) (*http.Client, error) {
	base := GetHttpClient()
	var baseTransport http.RoundTripper
	var client http.Client
	if base != nil {
		client = *base
		baseTransport = base.Transport
	}
	verifiedTransport, err := cloneAdtyVerifiedPaymentTransport(baseTransport)
	if err != nil {
		return nil, err
	}
	client.Transport = verifiedTransport
	if timeout > 0 {
		client.Timeout = timeout
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &client, nil
}
