package service

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/adty_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const ftrOpsPlatformFrom = "ADTY AI <noreply@mail.adtyai.com>"
const ftrOpsPlatformAddress = "noreply@mail.adtyai.com"

// ftrOpsRoundTripper is the same DefaultTransport stand-in the email tests use.
// resendEmailClient leaves Transport nil, so this intercepts delivery.
type ftrOpsRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip ftrOpsRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type ftrOpsMoney struct {
	users   int64
	quota   int64
	batches int64
	wallets int64
}

func ftrOpsMoneyNow(t *testing.T, db *gorm.DB) ftrOpsMoney {
	t.Helper()
	var snap ftrOpsMoney
	require.NoError(t, db.Model(&model.User{}).Count(&snap.users).Error)
	require.NoError(t, db.Model(&model.User{}).Select("COALESCE(SUM(quota), 0)").Scan(&snap.quota).Error)
	require.NoError(t, db.Model(&model.AdtyFundBatch{}).Count(&snap.batches).Error)
	require.NoError(t, db.Model(&model.AdtySiteWallet{}).Count(&snap.wallets).Error)
	return snap
}

func TestFTRADTY2305SiteMailSenderPG(t *testing.T) {
	db := prepareReferralDB(t)
	pgtest.RecordServerVersion(t, db)

	savedReseller := *adty_setting.GetResellerSetting()
	t.Cleanup(func() { *adty_setting.GetResellerSetting() = savedReseller })
	cfg := adty_setting.GetResellerSetting()
	cfg.SenderDomainEnabled = true
	common.OptionMapRWMutex.Lock()
	common.OptionMap["EmailProvider"] = "resend"
	common.OptionMap["ResendFrom"] = ftrOpsPlatformFrom
	common.OptionMap["ResendApiKey"] = "re_ftr2305"
	common.OptionMapRWMutex.Unlock()

	before := ftrOpsMoneyNow(t, db)

	var nilSender string
	require.NotPanics(t, func() { nilSender = AdtySenderFor(nil) })
	assert.Contains(t, nilSender, "<"+ftrOpsPlatformAddress+">")

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	site := &model.AdtySite{
		Subdomain:  "ftr2305" + suffix,
		RootDomain: "sites.example.com",
		Status:     model.AdtySiteStatusActive,
		OpenedBy:   model.AdtySiteOpenedByAdmin,
		Brand:      `{"name":"FTR Shop"}`,
	}
	require.NoError(t, db.Create(site).Error)
	require.NotZero(t, site.Id)

	var unverified string
	require.NotPanics(t, func() { unverified = AdtySenderFor(site) })
	assert.Contains(t, unverified, "FTR Shop")
	assert.Contains(t, unverified, "<"+ftrOpsPlatformAddress+">")

	domain := "sender." + site.Subdomain + ".example"
	require.NoError(t, db.Create(&model.AdtySiteSenderDomain{
		SiteId:         site.Id,
		Domain:         domain,
		ResendDomainId: "dom-" + site.Subdomain,
		Status:         model.AdtySenderDomainVerified,
	}).Error)
	verified := AdtySenderFor(site)
	assert.Contains(t, verified, "noreply@"+domain)
	assert.NotContains(t, verified, ftrOpsPlatformAddress)

	// Not a public send: the email tests intercept this client via DefaultTransport.
	previousTransport := http.DefaultTransport
	calls := 0
	var gotSubject, gotFrom string
	http.DefaultTransport = ftrOpsRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://api.resend.com/emails" {
			return nil, io.ErrUnexpectedEOF
		}
		calls++
		var body struct {
			From    string `json:"from"`
			Subject string `json:"subject"`
		}
		require.NoError(t, common.DecodeJson(request.Body, &body))
		gotSubject = body.Subject
		gotFrom = body.From
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"id":"email-ftr-2305"}`)),
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	const subject = "FTR-2305 site sender"
	require.NoError(t, AdtySendSiteEmail(site, subject, "receiver@example.com", "<p>ok</p>"))
	http.DefaultTransport = previousTransport
	assert.Equal(t, 1, calls)
	assert.Equal(t, subject, gotSubject)
	assert.Contains(t, gotFrom, "noreply@"+domain)

	cfg.SenderDomainEnabled = false
	disabled := AdtySenderFor(site)
	assert.NotContains(t, disabled, domain)
	assert.Contains(t, disabled, ftrOpsPlatformAddress)

	cfg.SenderDomainEnabled = true
	require.NoError(t, db.Model(&model.AdtySiteSenderDomain{}).Where("site_id = ?", site.Id).Update("status", model.AdtySenderDomainDisabled).Error)
	rowDisabled := AdtySenderFor(site)
	assert.NotContains(t, rowDisabled, domain)
	assert.Contains(t, rowDisabled, ftrOpsPlatformAddress)

	assert.Equal(t, before, ftrOpsMoneyNow(t, db))
}

func TestFTRADTY2306RedactAndRequestIDPG(t *testing.T) {
	db := prepareReferralDB(t)
	pgtest.RecordServerVersion(t, db)
	before := ftrOpsMoneyNow(t, db)
	var ticketsBefore, messagesBefore int64
	require.NoError(t, db.Model(&model.AdtyTicket{}).Count(&ticketsBefore).Error)
	require.NoError(t, db.Model(&model.AdtyTicketMessage{}).Count(&messagesBefore).Error)

	chinese := "这是一段普通的中文说明，没有凭据。"
	assert.Equal(t, chinese, redactAdtySecrets(chinese))

	token := "sk-" + "abcd1234"
	require.GreaterOrEqual(t, len(token)-len("sk-"), 8)
	mixed := redactAdtySecrets(chinese + " " + token)
	assert.Equal(t, chinese+" [redacted]", mixed)
	assert.NotContains(t, mixed, token)
	assert.Contains(t, mixed, chinese)

	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.aBcDeFgHiJkL"
	jwtGot := redactAdtySecrets("令牌 " + jwt + " 已失效")
	assert.Equal(t, "令牌 [redacted] 已失效", jwtGot)
	assert.NotContains(t, jwtGot, "eyJhbGciOiJIUzI1NiJ9")

	lone := "文档里单独出现 eyJ 并不构成令牌。"
	assert.Equal(t, lone, redactAdtySecrets(lone))
	assert.Contains(t, redactAdtySecrets(lone), "eyJ")

	passwordGot := redactAdtySecrets("登录失败 password=secret 请重试")
	assert.Equal(t, "登录失败 [redacted] 请重试", passwordGot)
	assert.NotContains(t, passwordGot, "secret")
	assert.NotContains(t, passwordGot, "password=secret")

	// middleware imports service, so RequestId cannot be called from this package.
	requestID := common.NewRequestId()
	require.NotEmpty(t, requestID)
	var mu sync.Mutex
	var lines []string
	restore := common.SetSysLogSink(func(level, message string) {
		mu.Lock()
		lines = append(lines, level+" "+message)
		mu.Unlock()
	})
	t.Cleanup(restore)
	common.SysError("request_id=" + requestID + " " + mixed)
	mu.Lock()
	joined := strings.Join(lines, "\n")
	mu.Unlock()
	require.NotEmpty(t, joined)
	assert.Contains(t, joined, "ERROR")
	assert.Contains(t, joined, requestID)
	assert.Contains(t, joined, "[redacted]")
	assert.Contains(t, joined, "普通的中文说明")
	assert.NotContains(t, joined, token)

	var ticketsAfter, messagesAfter int64
	require.NoError(t, db.Model(&model.AdtyTicket{}).Count(&ticketsAfter).Error)
	require.NoError(t, db.Model(&model.AdtyTicketMessage{}).Count(&messagesAfter).Error)
	assert.Equal(t, ticketsBefore, ticketsAfter)
	assert.Equal(t, messagesBefore, messagesAfter)
	assert.Equal(t, before, ftrOpsMoneyNow(t, db))
}
