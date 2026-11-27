package service

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"golang.org/x/crypto/acme"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	adtyLetsEncryptStagingURL      = "https://acme-staging-v02.api.letsencrypt.org/directory"
	adtyEdgeCertLeaseTTL           = 5 * time.Minute
	adtyEdgeCertWaitingRetry       = 2 * time.Minute
	adtyEdgeCertBatchLimit         = 10
	adtyEdgeCertCleanupGrace       = 7 * 24 * time.Hour
	adtyEdgeStateKeyStaleCertSince = "stale_cert_since"
)

var (
	errAdtyEdgeWaitingDNS = errors.New("edge acme cname not configured")

	adtyEdgeACMEDirectoryURL = func(staging bool) string {
		if staging {
			return adtyLetsEncryptStagingURL
		}
		return acme.LetsEncryptURL
	}
	adtyEdgeACMEHTTPClient *http.Client
	adtyEdgeDNSResolver    = &net.Resolver{PreferGo: true}
	adtyEdgeLookupCNAME    = func(ctx context.Context, name string) (string, error) {
		return adtyEdgeDNSResolver.LookupCNAME(ctx, name)
	}
	adtyEdgeLookupTXT = func(ctx context.Context, name string) ([]string, error) {
		return adtyEdgeDNSResolver.LookupTXT(ctx, name)
	}
	adtyEdgeSleep               = time.Sleep
	adtyEdgeTXTPropagateWait    = 3 * time.Second
	adtyEdgeTXTPropagatePolls   = 10
	adtyEdgeRenewBefore         = 30 * 24 * time.Hour
	adtyEdgePreserveReadyMargin = 24 * time.Hour
)

func adtyEdgeACMEEnvironment(cfg *adty_setting.ResellerSetting) string {
	if cfg != nil && cfg.EdgeACMEStaging {
		return model.AdtyEdgeACMEEnvStaging
	}
	return model.AdtyEdgeACMEEnvProduction
}

func adtyEdgeRootDomains(cfg *adty_setting.ResellerSetting) []string {
	if cfg == nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(cfg.RootDomains)+1)
	add := func(raw string) {
		host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
		if host == "" || seen[host] {
			return
		}
		seen[host] = true
		out = append(out, host)
	}
	add(cfg.AliyunDomain)
	for _, root := range cfg.RootDomains {
		add(root)
	}
	return out
}

func adtyEdgeWildcardAlias(root string, primary bool, suffix string) string {
	suffix = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(suffix)), ".")
	if primary {
		return "wild." + suffix
	}
	sum := sha256.Sum256([]byte(root))
	return "wild-" + hex.EncodeToString(sum[:4]) + "." + suffix
}

func adtyEdgeMainSiteHost(cfg *adty_setting.ResellerSetting) string {
	if cfg == nil || !cfg.EdgeMainSiteEnabled {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.EdgeMainSiteHost)), ".")
	if host == "" || AdtyIsMainAPIHost(host) || AdtyIsApiOnlyHost(host) {
		return ""
	}
	return host
}

func adtyEdgeMainSiteAlias(suffix string) string {
	suffix = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(suffix)), ".")
	if suffix == "" {
		return ""
	}
	return "main." + suffix
}

func adtyEdgeChallengeHost(certName string) string {
	base := strings.TrimPrefix(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(certName)), "."), "*.")
	return "_acme-challenge." + base
}

func adtyEdgeCertKeyPurpose(certName string) string {
	return "edge_cert_key:" + strings.ToLower(strings.TrimSpace(certName))
}

func adtyEdgeACMEAccountPurpose(env string) string {
	return "edge_acme_account:" + strings.ToLower(strings.TrimSpace(env))
}

func adtyEdgeCheckCNAME(ctx context.Context, challengeHost, expectedAlias string) (bool, string, error) {
	expected := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(expectedAlias)), ".")
	challenge := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(challengeHost)), ".")
	target, err := adtyEdgeLookupCNAME(ctx, challenge)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return false, "", nil
		}
		return false, "", err
	}
	actual := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(target)), ".")
	if actual == "" || actual == challenge {
		return false, "", nil
	}
	return actual == expected, actual, nil
}

func adtyWaitEdgeTXTPropagation(ctx context.Context, fqdn, expected string) {
	fqdn = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(fqdn)), ".")
	for attempt := 0; attempt < adtyEdgeTXTPropagatePolls; attempt++ {
		if ctx.Err() != nil {
			return
		}
		values, err := adtyEdgeLookupTXT(ctx, fqdn)
		if err == nil && slices.Contains(values, expected) {
			return
		}
		if attempt+1 < adtyEdgeTXTPropagatePolls && adtyEdgeTXTPropagateWait > 0 {
			adtyEdgeSleep(adtyEdgeTXTPropagateWait)
		}
	}
}

func adtyEdgeFailureBackoff(attempts int, err error) time.Duration {
	if d, ok := acme.RateLimit(err); ok && d > 0 {
		return min(max(d, time.Minute), 6*time.Hour)
	}
	steps := []time.Duration{
		5 * time.Minute,
		15 * time.Minute,
		30 * time.Minute,
		time.Hour,
		2 * time.Hour,
		4 * time.Hour,
		6 * time.Hour,
	}
	idx := min(max(attempts, 0), len(steps)-1)
	return steps[idx]
}

func adtyEdgeCertUserError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errAdtyEdgeWaitingDNS) {
		return strings.TrimPrefix(err.Error(), errAdtyEdgeWaitingDNS.Error()+": ")
	}
	var msg *common.Message
	if errors.As(err, &msg) {
		return msg.Error()
	}
	var cfErr *AdtyCloudflareError
	if errors.As(err, &cfErr) {
		return adtyCloudflareUserError(err).Error()
	}
	var acmeErr *acme.Error
	if errors.As(err, &acmeErr) {
		detail := strings.TrimSpace(acmeErr.Detail)
		if len(detail) > 220 {
			detail = detail[:220]
		}
		if detail != "" {
			return fmt.Sprintf("ACME error (%d): %s", acmeErr.StatusCode, detail)
		}
		return fmt.Sprintf("ACME error (%d)", acmeErr.StatusCode)
	}
	text := strings.TrimSpace(err.Error())
	if len(text) > 220 {
		text = text[:220]
	}
	return text
}

func adtyEncodeECPrivateKeyPEM(key *ecdsa.PrivateKey) (string, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})), nil
}

func adtyParseECPrivateKeyPEM(raw string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("invalid EC private key PEM")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

func adtyACMEHTTPClient() *http.Client {
	if adtyEdgeACMEHTTPClient != nil {
		return adtyEdgeACMEHTTPClient
	}
	return NewVerifiedDirectNoRedirectHTTPClient(30 * time.Second)
}

func adtyLoadOrRegisterACMEAccount(ctx context.Context, env string) (*acme.Client, error) {
	staging := env == model.AdtyEdgeACMEEnvStaging
	dirURL := adtyEdgeACMEDirectoryURL(staging)

	var row model.AdtyEdgeACMEAccount
	res := model.DB.WithContext(ctx).Where("environment = ?", env).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, res.Error
	}

	var key *ecdsa.PrivateKey
	var err error
	needSave := false
	if res.RowsAffected > 0 && strings.TrimSpace(row.KeyEncrypted) != "" {
		plainPEM, decErr := model.OpenAdtyPurposeSecret(adtyEdgeACMEAccountPurpose(env), row.KeyEncrypted)
		if decErr != nil {
			return nil, fmt.Errorf("decrypt edge acme account key: %w", decErr)
		}
		key, err = adtyParseECPrivateKeyPEM(plainPEM)
		if err != nil {
			return nil, err
		}
	} else {
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		pemStr, encErr := adtyEncodeECPrivateKeyPEM(key)
		if encErr != nil {
			return nil, encErr
		}
		sealed, sealErr := model.SealAdtyPurposeSecret(adtyEdgeACMEAccountPurpose(env), pemStr)
		if sealErr != nil {
			return nil, sealErr
		}
		row = model.AdtyEdgeACMEAccount{
			Environment:  env,
			KeyEncrypted: sealed,
		}
		needSave = true
	}

	client := &acme.Client{
		Key:          key,
		DirectoryURL: dirURL,
		HTTPClient:   adtyACMEHTTPClient(),
	}

	if strings.TrimSpace(row.AccountURI) == "" {
		acct, regErr := client.Register(ctx, &acme.Account{}, acme.AcceptTOS)
		if errors.Is(regErr, acme.ErrAccountAlreadyExists) {
			acct, regErr = client.GetReg(ctx, "")
		}
		if regErr != nil {
			return nil, regErr
		}
		if acct != nil {
			row.AccountURI = strings.TrimSpace(acct.URI)
		}
		needSave = true
	}

	if needSave {
		if err := model.DB.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "environment"}},
			DoUpdates: clause.AssignmentColumns([]string{"account_uri", "key_encrypted", "updated_at"}),
		}).Create(&row).Error; err != nil {
			return nil, err
		}
	}
	return client, nil
}

type adtyEdgeIssuedCert struct {
	CertPEM      string
	KeyEncrypted string
	NotBefore    int64
	NotAfter     int64
	Environment  string
}

func adtyIssueEdgeCert(ctx context.Context, cfg *adty_setting.ResellerSetting, cert *model.AdtyEdgeCert, env string) (*adtyEdgeIssuedCert, error) {
	if cert == nil || strings.TrimSpace(cert.CertName) == "" || strings.TrimSpace(cert.AliasFQDN) == "" {
		return nil, common.NewMessage("Edge certificate configuration is incomplete")
	}
	zoneID := strings.TrimSpace(cfg.CloudflareZoneId)
	if _, err := AdtyCloudflare.verifyHostInZone(ctx, zoneID, cert.AliasFQDN, false); err != nil {
		return nil, err
	}
	challengeHost := adtyEdgeChallengeHost(cert.CertName)
	matched, actual, err := adtyEdgeCheckCNAME(ctx, challengeHost, cert.AliasFQDN)
	if err != nil {
		return nil, fmt.Errorf("DNS CNAME check for %s failed: %w", challengeHost, err)
	}
	if !matched {
		if actual != "" {
			return nil, fmt.Errorf("%w: %s currently points to %s instead of %s", errAdtyEdgeWaitingDNS, challengeHost, actual, cert.AliasFQDN)
		}
		return nil, fmt.Errorf("%w: add CNAME %s -> %s", errAdtyEdgeWaitingDNS, challengeHost, cert.AliasFQDN)
	}

	client, err := adtyLoadOrRegisterACMEAccount(ctx, env)
	if err != nil {
		return nil, err
	}

	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(cert.CertName))
	if err != nil {
		return nil, err
	}

	var createdRecordIDs []string
	defer func() {
		if len(createdRecordIDs) == 0 {
			return
		}
		cleanCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, recID := range createdRecordIDs {
			if delErr := AdtyCloudflare.deleteDNSRecord(cleanCtx, zoneID, recID); delErr != nil {
				common.SysError(common.LogText("edge acme txt cleanup failed for %s: %s", cert.CertName, delErr.Error()))
			}
		}
	}()

	for _, authzURL := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, authzURL)
		if err != nil {
			return nil, err
		}
		if authz.Status == acme.StatusValid {
			continue
		}
		var dnsChal *acme.Challenge
		for _, ch := range authz.Challenges {
			if ch.Type == "dns-01" {
				dnsChal = ch
				break
			}
		}
		if dnsChal == nil {
			return nil, errors.New("ACME authorization does not offer dns-01 challenge")
		}
		txtVal, err := client.DNS01ChallengeRecord(dnsChal.Token)
		if err != nil {
			return nil, err
		}
		recID, err := AdtyCloudflare.createTXTRecord(ctx, zoneID, cert.AliasFQDN, txtVal)
		if err != nil {
			return nil, err
		}
		createdRecordIDs = append(createdRecordIDs, recID)

		adtyWaitEdgeTXTPropagation(ctx, cert.AliasFQDN, txtVal)

		if _, err := client.Accept(ctx, dnsChal); err != nil {
			return nil, err
		}
		if _, err := client.WaitAuthorization(ctx, authz.URI); err != nil {
			return nil, err
		}
	}

	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return nil, err
	}

	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		DNSNames: []string{cert.CertName},
	}, certKey)
	if err != nil {
		return nil, err
	}

	derChain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csrDER, true)
	if err != nil {
		return nil, err
	}
	if len(derChain) == 0 {
		return nil, errors.New("ACME server returned empty certificate chain")
	}
	leaf, err := x509.ParseCertificate(derChain[0])
	if err != nil {
		return nil, fmt.Errorf("parse issued certificate: %w", err)
	}
	verifyHost := cert.CertName
	if strings.HasPrefix(verifyHost, "*.") {
		verifyHost = "probe." + strings.TrimPrefix(verifyHost, "*.")
	}
	if err := leaf.VerifyHostname(verifyHost); err != nil {
		return nil, fmt.Errorf("issued certificate does not match %s: %w", cert.CertName, err)
	}

	var chainBuf bytes.Buffer
	for _, der := range derChain {
		if err := pem.Encode(&chainBuf, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
			return nil, err
		}
	}
	keyPEM, err := adtyEncodeECPrivateKeyPEM(certKey)
	if err != nil {
		return nil, err
	}
	sealedKey, err := model.SealAdtyPurposeSecret(adtyEdgeCertKeyPurpose(cert.CertName), keyPEM)
	if err != nil {
		return nil, err
	}
	return &adtyEdgeIssuedCert{
		CertPEM:      chainBuf.String(),
		KeyEncrypted: sealedKey,
		NotBefore:    leaf.NotBefore.Unix(),
		NotAfter:     leaf.NotAfter.Unix(),
		Environment:  env,
	}, nil
}

func adtyEnsureEdgeCertRows(ctx context.Context, cfg *adty_setting.ResellerSetting, env string, now time.Time) ([]string, error) {
	suffix := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.EdgeACMEAliasSuffix)), ".")
	if suffix == "" {
		return nil, nil
	}

	activeCertSet := make(map[string]bool)
	activeCertNames := make([]string, 0)
	recordActive := func(name string) {
		n := strings.ToLower(strings.TrimSpace(name))
		if n == "" || activeCertSet[n] {
			return
		}
		activeCertSet[n] = true
		activeCertNames = append(activeCertNames, n)
	}

	roots := adtyEdgeRootDomains(cfg)
	for i, root := range roots {
		certName := "*." + root
		aliasFQDN := adtyEdgeWildcardAlias(root, i == 0, suffix)
		if err := adtyUpsertEdgeCertRow(ctx, certName, model.AdtyEdgeCertKindWildcard, 0, aliasFQDN, env); err != nil {
			return nil, err
		}
		recordActive(certName)
	}

	mainHost := adtyEdgeMainSiteHost(cfg)
	if mainHost != "" {
		if err := adtyUpsertEdgeCertRow(ctx, mainHost, model.AdtyEdgeCertKindMain, 0, adtyEdgeMainSiteAlias(suffix), env); err != nil {
			return nil, err
		}
		recordActive(mainHost)
	}

	var domains []model.AdtySiteDomain
	if err := model.DB.WithContext(ctx).
		Joins("JOIN adty_sites ON adty_sites.id = adty_site_domains.site_id").
		Where("adty_site_domains.managed_by <> ? AND adty_site_domains.role = ? AND adty_site_domains.status IN ? AND adty_site_domains.desired_state IN ? AND adty_sites.status <> ?",
			"platform", model.AdtySiteDomainRoleSite, []string{model.AdtySiteDomainPending, model.AdtySiteDomainActive}, []string{"", "serve", "redirect_only"}, model.AdtySiteStatusDeleting).
		Order("adty_site_domains.id ASC").Find(&domains).Error; err != nil {
		return nil, err
	}

	activeDomainIDs := make(map[int]bool, len(domains))
	for i := range domains {
		dom := &domains[i]
		activeDomainIDs[dom.Id] = true
		host := strings.ToLower(strings.TrimSpace(dom.Hostname))
		if host == "" || (mainHost != "" && host == mainHost) {
			continue
		}
		alias := strings.TrimSpace(dom.EdgeACMEAlias)
		if alias == "" {
			generated, err := adtyGenerateEdgeACMEAlias(dom.Id)
			if err != nil {
				return nil, err
			}
			res := model.DB.WithContext(ctx).Model(&model.AdtySiteDomain{}).
				Where("id = ? AND edge_acme_alias = ?", dom.Id, "").
				Update("edge_acme_alias", generated)
			if res.Error != nil {
				return nil, res.Error
			}
			if res.RowsAffected > 0 {
				alias = generated
			} else {
				var fresh model.AdtySiteDomain
				if err := model.DB.WithContext(ctx).Select("edge_acme_alias").First(&fresh, dom.Id).Error; err == nil {
					alias = strings.TrimSpace(fresh.EdgeACMEAlias)
				}
			}
		}
		if alias == "" {
			continue
		}
		aliasFQDN := strings.ToLower(alias) + "." + suffix
		if err := adtyUpsertEdgeCertRow(ctx, host, model.AdtyEdgeCertKindCustom, dom.Id, aliasFQDN, env); err != nil {
			return nil, err
		}
		recordActive(host)
	}

	// Clean up stale wildcard/main cert rows only after a true 7-day grace window
	// measured from when each cert first became inactive (persisted in adty_edge_states),
	// so temporarily toggling settings never deletes an existing valid cert immediately.
	nowUnix := now.Unix()
	cutoff := now.Add(-adtyEdgeCertCleanupGrace).Unix()
	graceSecs := int64(adtyEdgeCertCleanupGrace.Seconds())
	hasStateTable, err := model.AdtyTableExists(model.DB.WithContext(ctx), &model.AdtyEdgeState{})
	if err != nil {
		return nil, err
	}
	staleSince := make(map[string]int64)
	stateDirty := false
	if hasStateTable {
		var st model.AdtyEdgeState
		res := model.DB.WithContext(ctx).Where("state_key = ?", adtyEdgeStateKeyStaleCertSince).Limit(1).Find(&st)
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected > 0 && strings.TrimSpace(st.Value) != "" {
			_ = common.Unmarshal([]byte(st.Value), &staleSince)
		}
		for k := range staleSince {
			if activeCertSet[k] {
				delete(staleSince, k)
				stateDirty = true
			}
		}
	}

	var managedCerts []model.AdtyEdgeCert
	if err := model.DB.WithContext(ctx).
		Where("kind IN ?", []string{model.AdtyEdgeCertKindWildcard, model.AdtyEdgeCertKindMain}).
		Find(&managedCerts).Error; err != nil {
		return nil, err
	}
	inactiveManaged := make(map[string]bool)
	for _, cert := range managedCerts {
		key := strings.ToLower(strings.TrimSpace(cert.CertName))
		if activeCertSet[key] {
			continue
		}
		inactiveManaged[key] = true
		if hasStateTable {
			since := staleSince[key]
			if since <= 0 {
				staleSince[key] = nowUnix
				stateDirty = true
				continue
			}
			if nowUnix-since < graceSecs {
				continue
			}
			if err := model.DB.WithContext(ctx).Delete(&model.AdtyEdgeCert{}, cert.Id).Error; err != nil {
				return nil, err
			}
			delete(staleSince, key)
			stateDirty = true
		} else if cert.UpdatedAt > 0 && cert.UpdatedAt <= cutoff {
			if err := model.DB.WithContext(ctx).Delete(&model.AdtyEdgeCert{}, cert.Id).Error; err != nil {
				return nil, err
			}
		}
	}
	if hasStateTable {
		for k := range staleSince {
			if !inactiveManaged[k] {
				delete(staleSince, k)
				stateDirty = true
			}
		}
		if stateDirty {
			row := model.AdtyEdgeState{
				StateKey:  adtyEdgeStateKeyStaleCertSince,
				Value:     common.GetJsonString(staleSince),
				UpdatedAt: nowUnix,
			}
			if err := model.DB.WithContext(ctx).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "state_key"}},
				DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
			}).Create(&row).Error; err != nil {
				return nil, err
			}
		}
	}

	var customCerts []model.AdtyEdgeCert
	if err := model.DB.WithContext(ctx).
		Where("kind = ? AND updated_at > 0 AND updated_at <= ?", model.AdtyEdgeCertKindCustom, cutoff).
		Find(&customCerts).Error; err != nil {
		return nil, err
	}
	for _, cert := range customCerts {
		if cert.DomainId > 0 && activeDomainIDs[cert.DomainId] {
			continue
		}
		var dom model.AdtySiteDomain
		res := model.DB.WithContext(ctx).Select("id", "status", "updated_at").Where("id = ?", cert.DomainId).Limit(1).Find(&dom)
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected == 0 || (dom.Status == model.AdtySiteDomainRemoved && dom.UpdatedAt <= cutoff) {
			if err := model.DB.WithContext(ctx).Delete(&model.AdtyEdgeCert{}, cert.Id).Error; err != nil {
				return nil, err
			}
		}
	}
	return activeCertNames, nil
}

func adtyUpsertEdgeCertRow(ctx context.Context, certName, kind string, domainID int, aliasFQDN, env string) error {
	var existing model.AdtyEdgeCert
	res := model.DB.WithContext(ctx).Where("cert_name = ?", certName).Limit(1).Find(&existing)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		row := model.AdtyEdgeCert{
			CertName:    certName,
			Kind:        kind,
			DomainId:    domainID,
			AliasFQDN:   aliasFQDN,
			Environment: env,
			Status:      model.AdtyEdgeCertStatusWaitingDNS,
			NextRunAt:   0,
		}
		if err := model.DB.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "cert_name"}},
			DoNothing: true,
		}).Create(&row).Error; err != nil {
			return err
		}
		if domainID > 0 {
			q := model.DB.WithContext(ctx).Model(&model.AdtySiteDomain{}).
				Where("id = ? AND status <> ? AND edge_cert_status = ?", domainID, model.AdtySiteDomainRemoved, "")
			if kind == model.AdtyEdgeCertKindCustom {
				aliasLabel := adtyEdgeAliasFirstLabel(aliasFQDN)
				if aliasLabel == "" {
					return nil
				}
				q = q.Where("LOWER(TRIM(edge_acme_alias)) = ?", aliasLabel)
			}
			_ = q.Update("edge_cert_status", model.AdtyEdgeCertStatusWaitingDNS).Error
		}
		return nil
	}
	updates := map[string]any{}
	needsReissue := false
	existingEnv := strings.TrimSpace(existing.Environment)
	if existingEnv == "" {
		existingEnv = model.AdtyEdgeACMEEnvProduction
	}
	if existing.AliasFQDN != aliasFQDN {
		updates["alias_fqdn"] = aliasFQDN
		updates["next_run_at"] = 0
		if kind == model.AdtyEdgeCertKindCustom && adtyEdgeAliasFirstLabel(existing.AliasFQDN) != adtyEdgeAliasFirstLabel(aliasFQDN) {
			needsReissue = true
		} else if existing.Status != model.AdtyEdgeCertStatusReady || strings.TrimSpace(existing.CertPEM) == "" || strings.TrimSpace(existing.KeyEncrypted) == "" {
			updates["status"] = model.AdtyEdgeCertStatusWaitingDNS
			updates["last_error"] = ""
			updates["attempts"] = 0
			existing.Status = model.AdtyEdgeCertStatusWaitingDNS
			existing.LastError = ""
		}
	}
	if existing.Kind != kind {
		updates["kind"] = kind
	}
	if existing.DomainId != domainID {
		updates["domain_id"] = domainID
		needsReissue = true
	}
	if existing.Environment != env {
		updates["environment"] = env
		if existingEnv != env {
			needsReissue = true
		}
	}
	if needsReissue {
		updates["status"] = model.AdtyEdgeCertStatusWaitingDNS
		updates["cert_pem"] = ""
		updates["key_encrypted"] = ""
		updates["not_before"] = 0
		updates["not_after"] = 0
		updates["last_issued_at"] = 0
		updates["lease_token"] = ""
		updates["lease_until"] = 0
		updates["last_error"] = ""
		updates["attempts"] = 0
		updates["next_run_at"] = 0
		existing.Status = model.AdtyEdgeCertStatusWaitingDNS
		existing.LastError = ""
		existing.NotAfter = 0
	}
	if len(updates) > 0 {
		if err := model.DB.WithContext(ctx).Model(&model.AdtyEdgeCert{}).Where("id = ?", existing.Id).Updates(updates).Error; err != nil {
			return err
		}
	}
	if domainID > 0 {
		adtySyncDomainEdgeCertColumns(ctx, &model.AdtyEdgeCert{
			Kind:      kind,
			DomainId:  domainID,
			AliasFQDN: aliasFQDN,
		}, existing.Status, existing.LastError, existing.NotAfter)
	}
	return nil
}

// adtyInvalidateCustomEdgeCertTx removes any custom edge certificate row matching
// domainID or hostname inside the caller's transaction so stale cert/key material
// and in-flight renewal leases cannot survive domain removal or rebind.
func adtyInvalidateCustomEdgeCertTx(tx *gorm.DB, domainID int, hostname string) error {
	if tx == nil {
		return nil
	}
	hasCertTable, err := model.AdtyTableExists(tx, &model.AdtyEdgeCert{})
	if err != nil {
		return err
	}
	if !hasCertTable {
		return nil
	}
	host := strings.ToLower(strings.TrimSpace(hostname))
	switch {
	case domainID > 0 && host != "":
		return tx.Where("kind = ? AND (domain_id = ? OR cert_name = ?)", model.AdtyEdgeCertKindCustom, domainID, host).
			Delete(&model.AdtyEdgeCert{}).Error
	case domainID > 0:
		return tx.Where("kind = ? AND domain_id = ?", model.AdtyEdgeCertKindCustom, domainID).
			Delete(&model.AdtyEdgeCert{}).Error
	case host != "":
		return tx.Where("kind = ? AND cert_name = ?", model.AdtyEdgeCertKindCustom, host).
			Delete(&model.AdtyEdgeCert{}).Error
	default:
		return nil
	}
}

func adtyEdgeAliasFirstLabel(fqdn string) string {
	clean := strings.ToLower(strings.TrimSpace(fqdn))
	if idx := strings.IndexByte(clean, '.'); idx >= 0 {
		return clean[:idx]
	}
	return clean
}

func adtySyncDomainEdgeCertColumns(ctx context.Context, cert *model.AdtyEdgeCert, status, lastError string, notAfter int64) {
	if cert == nil || cert.DomainId <= 0 {
		return
	}
	q := model.DB.WithContext(ctx).Model(&model.AdtySiteDomain{}).
		Where("id = ? AND status <> ?", cert.DomainId, model.AdtySiteDomainRemoved)
	if cert.Kind == model.AdtyEdgeCertKindCustom {
		aliasLabel := adtyEdgeAliasFirstLabel(cert.AliasFQDN)
		if aliasLabel == "" {
			return
		}
		q = q.Where("LOWER(TRIM(edge_acme_alias)) = ?", aliasLabel)
	}
	_ = q.Updates(map[string]any{
		"edge_cert_status":    status,
		"edge_cert_error":     lastError,
		"edge_cert_not_after": notAfter,
	}).Error
}

// RunAdtyEdgeCertOnce reconciles wildcard and custom website edge certificates.
// It uses per-row leases so network I/O never runs inside a DB transaction.
func RunAdtyEdgeCertOnce(ctx context.Context, now time.Time) error {
	cfg := resellerSetting()
	if !cfg.EdgeMainlandEnabled || !cfg.CloudflareConfigured() || strings.TrimSpace(cfg.EdgeACMEAliasSuffix) == "" {
		return nil
	}
	env := adtyEdgeACMEEnvironment(cfg)
	activeCertNames, err := adtyEnsureEdgeCertRows(ctx, cfg, env, now)
	if err != nil {
		return err
	}
	if len(activeCertNames) == 0 {
		return nil
	}

	renewCutoff := now.Add(adtyEdgeRenewBefore).Unix()
	nowUnix := now.Unix()
	var candidates []model.AdtyEdgeCert
	if err := model.DB.WithContext(ctx).
		Where("cert_name IN ?", activeCertNames).
		Where("next_run_at <= ? AND lease_until <= ?", nowUnix, nowUnix).
		Where("status <> ? OR cert_pem = ? OR key_encrypted = ? OR environment <> ? OR not_after <= ?",
			model.AdtyEdgeCertStatusReady, "", "", env, renewCutoff).
		Order("next_run_at ASC, id ASC").Limit(adtyEdgeCertBatchLimit).Find(&candidates).Error; err != nil {
		return err
	}

	activeWildcardCerts := make(map[string]bool)
	for _, root := range adtyEdgeRootDomains(cfg) {
		if r := strings.ToLower(strings.TrimSpace(root)); r != "" {
			activeWildcardCerts["*."+r] = true
		}
	}
	var failures []error
	started := time.Now()
	for i := range candidates {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		row := &candidates[i]
		// Skip wildcard certs whose root domain is no longer configured.
		if row.Kind == model.AdtyEdgeCertKindWildcard {
			if !activeWildcardCerts[strings.ToLower(strings.TrimSpace(row.CertName))] {
				continue
			}
		}
		// Skip main site certs if main site acceleration is disabled or switched to another hostname.
		if row.Kind == model.AdtyEdgeCertKindMain {
			if mainHost := adtyEdgeMainSiteHost(cfg); mainHost == "" || !strings.EqualFold(row.CertName, mainHost) {
				continue
			}
		}
		// Skip custom certs whose domain is no longer active/serve/redirect_only or whose alias rotated.
		if row.Kind == model.AdtyEdgeCertKindCustom {
			aliasLabel := adtyEdgeAliasFirstLabel(row.AliasFQDN)
			if aliasLabel == "" {
				continue
			}
			var activeCount int64
			if err := model.DB.WithContext(ctx).Model(&model.AdtySiteDomain{}).
				Joins("JOIN adty_sites ON adty_sites.id = adty_site_domains.site_id").
				Where("adty_site_domains.id = ? AND adty_site_domains.status IN ? AND adty_site_domains.desired_state IN ? AND LOWER(TRIM(adty_site_domains.edge_acme_alias)) = ? AND adty_sites.status <> ?",
					row.DomainId, []string{model.AdtySiteDomainPending, model.AdtySiteDomainActive}, []string{"", "serve", "redirect_only"}, aliasLabel, model.AdtySiteStatusDeleting).
				Count(&activeCount).Error; err != nil {
				return err
			}
			if activeCount == 0 {
				continue
			}
		}

		claimTime := now.Add(time.Since(started))
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		leaseToken := hex.EncodeToString(nonce)
		claim := model.DB.WithContext(ctx).Model(&model.AdtyEdgeCert{}).
			Where("id = ? AND lease_until <= ? AND next_run_at <= ?", row.Id, claimTime.Unix(), claimTime.Unix()).
			Updates(map[string]any{
				"lease_token": leaseToken,
				"lease_until": claimTime.Add(adtyEdgeCertLeaseTTL).Unix(),
			})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			continue
		}

		var fresh model.AdtyEdgeCert
		if err := model.DB.WithContext(ctx).Where("id = ? AND lease_token = ?", row.Id, leaseToken).First(&fresh).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}

		preserveReady := fresh.Status == model.AdtyEdgeCertStatusReady &&
			fresh.Environment == env &&
			fresh.CertPEM != "" &&
			fresh.KeyEncrypted != "" &&
			fresh.NotAfter > claimTime.Add(adtyEdgePreserveReadyMargin).Unix()

		if !preserveReady && fresh.Status != model.AdtyEdgeCertStatusWaitingDNS {
			issuingRes := model.DB.WithContext(ctx).Model(&model.AdtyEdgeCert{}).
				Where("id = ? AND lease_token = ?", fresh.Id, leaseToken).
				Update("status", model.AdtyEdgeCertStatusIssuing)
			if issuingRes.Error == nil && issuingRes.RowsAffected == 1 {
				adtySyncDomainEdgeCertColumns(ctx, &fresh, model.AdtyEdgeCertStatusIssuing, fresh.LastError, fresh.NotAfter)
			}
		}

		runCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		issued, issueErr := adtyIssueEdgeCert(runCtx, cfg, &fresh, env)
		cancel()

		finished := now.Add(time.Since(started))
		updates := map[string]any{
			"lease_token": "",
			"lease_until": 0,
		}
		var syncStatus, syncErr string
		var syncNotAfter int64
		if issueErr == nil {
			updates["status"] = model.AdtyEdgeCertStatusReady
			updates["environment"] = issued.Environment
			updates["cert_pem"] = issued.CertPEM
			updates["key_encrypted"] = issued.KeyEncrypted
			updates["not_before"] = issued.NotBefore
			updates["not_after"] = issued.NotAfter
			updates["last_error"] = ""
			updates["attempts"] = 0
			updates["last_issued_at"] = finished.Unix()
			nextRenew := time.Unix(issued.NotAfter, 0).Add(-adtyEdgeRenewBefore)
			if nextRenew.Before(finished.Add(time.Hour)) {
				nextRenew = finished.Add(time.Hour)
			}
			updates["next_run_at"] = nextRenew.Unix()
			syncStatus = model.AdtyEdgeCertStatusReady
			syncErr = ""
			syncNotAfter = issued.NotAfter
		} else if errors.Is(issueErr, errAdtyEdgeWaitingDNS) {
			nextStatus := model.AdtyEdgeCertStatusWaitingDNS
			if preserveReady {
				nextStatus = model.AdtyEdgeCertStatusReady
			}
			msg := adtyEdgeCertUserError(issueErr)
			updates["status"] = nextStatus
			updates["last_error"] = msg
			updates["next_run_at"] = finished.Add(adtyEdgeCertWaitingRetry).Unix()
			syncStatus = nextStatus
			syncErr = msg
			syncNotAfter = fresh.NotAfter
		} else {
			nextStatus := model.AdtyEdgeCertStatusFailed
			if preserveReady {
				nextStatus = model.AdtyEdgeCertStatusReady
			}
			msg := adtyEdgeCertUserError(issueErr)
			backoff := adtyEdgeFailureBackoff(fresh.Attempts, issueErr)
			updates["status"] = nextStatus
			updates["last_error"] = msg
			updates["attempts"] = fresh.Attempts + 1
			updates["next_run_at"] = finished.Add(backoff).Unix()
			syncStatus = nextStatus
			syncErr = msg
			syncNotAfter = fresh.NotAfter
			failures = append(failures, fmt.Errorf("edge cert %s: %w", fresh.CertName, issueErr))
		}

		res := model.DB.WithContext(ctx).Model(&model.AdtyEdgeCert{}).
			Where("id = ? AND lease_token = ?", fresh.Id, leaseToken).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 1 {
			adtySyncDomainEdgeCertColumns(ctx, &fresh, syncStatus, syncErr, syncNotAfter)
		}
	}
	return errors.Join(failures...)
}

// CheckAdtyCustomEdgeCertNow performs an immediate ACME CNAME check and certificate
// issuance attempt for a single custom website domain, bypassing retry backoff while
// keeping the same per-row lease isolation as RunAdtyEdgeCertOnce.
// When onlyIfCNAMEConfigured is true, it checks DNS first and returns nil without
// modifying error fields if the owner has not opted into Mainland China acceleration.
func CheckAdtyCustomEdgeCertNow(ctx context.Context, domain *model.AdtySiteDomain, onlyIfCNAMEConfigured bool) error {
	cfg := resellerSetting()
	if domain == nil || domain.Id <= 0 || domain.ManagedBy == "platform" || domain.Role != model.AdtySiteDomainRoleSite {
		return nil
	}
	if !cfg.EdgeMainlandEnabled || !cfg.CloudflareConfigured() {
		return nil
	}
	suffix := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.EdgeACMEAliasSuffix)), ".")
	certName := strings.ToLower(strings.TrimSpace(domain.Hostname))
	if suffix == "" || certName == "" {
		return nil
	}
	hasCertTable, err := model.AdtyTableExists(model.DB.WithContext(ctx), &model.AdtyEdgeCert{})
	if err != nil || !hasCertTable {
		return err
	}
	alias := strings.TrimSpace(domain.EdgeACMEAlias)
	if alias == "" {
		generated, genErr := adtyGenerateEdgeACMEAlias(domain.Id)
		if genErr != nil {
			return genErr
		}
		res := model.DB.WithContext(ctx).Model(&model.AdtySiteDomain{}).
			Where("id = ? AND edge_acme_alias = ?", domain.Id, "").
			Update("edge_acme_alias", generated)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			alias = generated
			domain.EdgeACMEAlias = generated
		} else {
			var fresh model.AdtySiteDomain
			if err := model.DB.WithContext(ctx).Select("edge_acme_alias").First(&fresh, domain.Id).Error; err == nil {
				alias = strings.TrimSpace(fresh.EdgeACMEAlias)
				domain.EdgeACMEAlias = alias
			}
		}
	}
	if alias == "" {
		return nil
	}
	aliasFQDN := strings.ToLower(alias) + "." + suffix
	if onlyIfCNAMEConfigured {
		matched, _, dnsErr := adtyEdgeCheckCNAME(ctx, adtyEdgeChallengeHost(certName), aliasFQDN)
		if dnsErr != nil || !matched {
			return nil
		}
	}
	env := adtyEdgeACMEEnvironment(cfg)
	if err := adtyUpsertEdgeCertRow(ctx, certName, model.AdtyEdgeCertKindCustom, domain.Id, aliasFQDN, env); err != nil {
		return err
	}
	now := time.Now()
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	leaseToken := hex.EncodeToString(nonce)
	claim := model.DB.WithContext(ctx).Model(&model.AdtyEdgeCert{}).
		Where("cert_name = ? AND lease_until <= ?", certName, now.Unix()).
		Updates(map[string]any{
			"lease_token": leaseToken,
			"lease_until": now.Add(adtyEdgeCertLeaseTTL).Unix(),
		})
	if claim.Error != nil {
		return claim.Error
	}
	if claim.RowsAffected == 0 {
		if onlyIfCNAMEConfigured {
			return nil
		}
		return common.NewMessage("A certificate check is already in progress for this domain. Please try again in a few seconds.")
	}
	var fresh model.AdtyEdgeCert
	if err := model.DB.WithContext(ctx).Where("cert_name = ? AND lease_token = ?", certName, leaseToken).First(&fresh).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	preserveReady := fresh.Status == model.AdtyEdgeCertStatusReady &&
		fresh.Environment == env &&
		fresh.CertPEM != "" &&
		fresh.KeyEncrypted != "" &&
		fresh.NotAfter > now.Add(adtyEdgePreserveReadyMargin).Unix()
	if preserveReady {
		_ = model.DB.WithContext(ctx).Model(&model.AdtyEdgeCert{}).
			Where("id = ? AND lease_token = ?", fresh.Id, leaseToken).
			Updates(map[string]any{"lease_token": "", "lease_until": 0}).Error
		adtySyncDomainEdgeCertColumns(ctx, &fresh, model.AdtyEdgeCertStatusReady, "", fresh.NotAfter)
		if !onlyIfCNAMEConfigured {
			_ = model.DB.WithContext(ctx).Model(&model.AdtySiteDomain{}).Where("id = ?", domain.Id).Update("last_checked_at", now.Unix()).Error
		}
		return nil
	}
	issued, issueErr := adtyIssueEdgeCert(ctx, cfg, &fresh, env)
	finished := time.Now()
	updates := map[string]any{
		"lease_token": "",
		"lease_until": 0,
	}
	var syncStatus, syncErr string
	var syncNotAfter int64
	if issueErr == nil {
		updates["status"] = model.AdtyEdgeCertStatusReady
		updates["environment"] = issued.Environment
		updates["cert_pem"] = issued.CertPEM
		updates["key_encrypted"] = issued.KeyEncrypted
		updates["not_before"] = issued.NotBefore
		updates["not_after"] = issued.NotAfter
		updates["last_error"] = ""
		updates["attempts"] = 0
		updates["last_issued_at"] = finished.Unix()
		nextRenew := time.Unix(issued.NotAfter, 0).Add(-adtyEdgeRenewBefore)
		if nextRenew.Before(finished.Add(time.Hour)) {
			nextRenew = finished.Add(time.Hour)
		}
		updates["next_run_at"] = nextRenew.Unix()
		syncStatus = model.AdtyEdgeCertStatusReady
		syncErr = ""
		syncNotAfter = issued.NotAfter
	} else if errors.Is(issueErr, errAdtyEdgeWaitingDNS) {
		msg := adtyEdgeCertUserError(issueErr)
		updates["status"] = model.AdtyEdgeCertStatusWaitingDNS
		updates["last_error"] = msg
		updates["next_run_at"] = finished.Add(adtyEdgeCertWaitingRetry).Unix()
		syncStatus = model.AdtyEdgeCertStatusWaitingDNS
		syncErr = msg
		syncNotAfter = fresh.NotAfter
	} else {
		msg := adtyEdgeCertUserError(issueErr)
		backoff := adtyEdgeFailureBackoff(fresh.Attempts, issueErr)
		updates["status"] = model.AdtyEdgeCertStatusFailed
		updates["last_error"] = msg
		updates["attempts"] = fresh.Attempts + 1
		updates["next_run_at"] = finished.Add(backoff).Unix()
		syncStatus = model.AdtyEdgeCertStatusFailed
		syncErr = msg
		syncNotAfter = fresh.NotAfter
	}
	res := model.DB.WithContext(ctx).Model(&model.AdtyEdgeCert{}).
		Where("id = ? AND lease_token = ?", fresh.Id, leaseToken).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 1 {
		adtySyncDomainEdgeCertColumns(ctx, &fresh, syncStatus, syncErr, syncNotAfter)
	}
	if !onlyIfCNAMEConfigured {
		_ = model.DB.WithContext(ctx).Model(&model.AdtySiteDomain{}).Where("id = ?", domain.Id).Update("last_checked_at", finished.Unix()).Error
	}
	return nil
}

// RetryAdtyEdgeCert resets backoff on one certificate (or the primary wildcard cert if empty).
func RetryAdtyEdgeCert(certName string) error {
	cfg := resellerSetting()
	target := strings.ToLower(strings.TrimSpace(certName))
	if target == "" {
		roots := adtyEdgeRootDomains(cfg)
		if len(roots) == 0 {
			return common.NewMessage("No root domain is configured")
		}
		target = "*." + roots[0]
	}
	var existing model.AdtyEdgeCert
	res := model.DB.Where("cert_name = ?", target).Limit(1).Find(&existing)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		if _, err := adtyEnsureEdgeCertRows(context.Background(), cfg, adtyEdgeACMEEnvironment(cfg), time.Now()); err != nil {
			return err
		}
		res = model.DB.Where("cert_name = ?", target).Limit(1).Find(&existing)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return common.NewMessage("Edge certificate record not found")
		}
	}
	if err := model.DB.Model(&model.AdtyEdgeCert{}).Where("id = ?", existing.Id).Updates(map[string]any{
		"next_run_at": 0,
		"last_error":  "",
	}).Error; err != nil {
		return err
	}
	if existing.DomainId > 0 {
		q := model.DB.Model(&model.AdtySiteDomain{}).Where("id = ? AND status <> ?", existing.DomainId, model.AdtySiteDomainRemoved)
		if existing.Kind == model.AdtyEdgeCertKindCustom {
			aliasLabel := adtyEdgeAliasFirstLabel(existing.AliasFQDN)
			if aliasLabel == "" {
				return nil
			}
			q = q.Where("LOWER(TRIM(edge_acme_alias)) = ?", aliasLabel)
		}
		_ = q.Update("edge_cert_error", "").Error
	}
	return nil
}

// GetAdtyEdgeWildcardCert returns the primary wildcard certificate row if present.
func GetAdtyEdgeWildcardCert(cfg *adty_setting.ResellerSetting) (*model.AdtyEdgeCert, error) {
	roots := adtyEdgeRootDomains(cfg)
	if len(roots) == 0 {
		return nil, nil
	}
	certName := "*." + roots[0]
	var row model.AdtyEdgeCert
	res := model.DB.Where("cert_name = ?", certName).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &row, nil
}

// GetAdtyEdgeMainSiteCert returns the main site certificate row if enabled and present.
func GetAdtyEdgeMainSiteCert(cfg *adty_setting.ResellerSetting) (*model.AdtyEdgeCert, error) {
	mainHost := adtyEdgeMainSiteHost(cfg)
	if mainHost == "" {
		return nil, nil
	}
	var row model.AdtyEdgeCert
	res := model.DB.Where("cert_name = ?", mainHost).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &row, nil
}

func startAdtyEdgeCertTask() {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			if err := RunAdtyEdgeCertOnce(ctx, time.Now()); err != nil {
				common.SysError(common.LogText("adty edge cert task: %s", err.Error()))
			}
			cancel()
		}
	}()
}

func init() {
	adtyMasterTasks = append(adtyMasterTasks, startAdtyEdgeCertTask)
}
