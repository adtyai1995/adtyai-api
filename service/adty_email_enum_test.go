package service

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ISS-R1-0114: A.22.2 系统事务邮件失败不回滚已提交业务，同时保留失败状态和可重试记录；
// 进程重启不静默丢失待发送任务；覆盖注册验证码/密码重置/邮箱绑定/安全与订单通知的允许收件人、
// 语言、过期和重试；不得保存真实 token 到证据或恢复退休营销。
func TestAdtySystemEmailDurableOutboxRetryExpiryRedactionAndRecipientPolicy(t *testing.T) {
	require.NoError(t, i18n.Init())
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := prepareReferralDialectDB(t, dialect)
			siteOwner := referralUser(t, db, "mail-site-owner")
			site := model.AdtySite{
				OwnerUserId: siteOwner.Id, Subdomain: "mailshop", RootDomain: "sites.example.com",
				Status: model.AdtySiteStatusActive, Brand: `{"name":"Mail Shop"}`,
			}
			require.NoError(t, db.Create(&site).Error)
			InvalidateAdtySiteCache()

			customer := referralUser(t, db, "mail-customer")
			require.NoError(t, db.Model(customer).Updates(map[string]any{
				"site_id": customer.SiteId,
				"email":   "customer@example.com",
				"quota":   500000,
			}).Error)
			require.NoError(t, model.UpdateUserSetting(customer.Id, dto.UserSetting{
				Language:          "zh-CN",
				NotifyType:        "email",
				NotificationEmail: "billing-notify@example.com",
			}))

			siteCustomer := referralUser(t, db, "mail-site-customer")
			require.NoError(t, db.Model(siteCustomer).Updates(map[string]any{
				"site_id": site.Id,
				"email":   "siteuser@example.com",
			}).Error)
			require.NoError(t, model.UpdateUserSetting(siteCustomer.Id, dto.UserSetting{
				Language: "en",
			}))

			var mu sync.Mutex
			type deliveredMessage struct {
				from, subject, to, content string
			}
			var delivered []deliveredMessage
			failDelivery := true

			oldSender := AdtySystemEmailSender
			oldDispatch := AdtySystemEmailAsyncDispatch
			AdtySystemEmailSender = func(from, subject, to, content string) error {
				mu.Lock()
				defer mu.Unlock()
				if failDelivery {
					return errors.New("smtp temporary 451 try again later")
				}
				delivered = append(delivered, deliveredMessage{from: from, subject: subject, to: to, content: content})
				return nil
			}
			t.Cleanup(func() {
				WaitAdtySystemEmailAsync()
				AdtySystemEmailSender = oldSender
				AdtySystemEmailAsyncDispatch = oldDispatch
			})

			baseNow := time.Now().UTC().Truncate(time.Second)

			// 1. 禁止恢复退役营销邮件
			for _, bad := range []AdtySystemEmailRequest{
				{Category: "marketing", Recipient: "customer@example.com", Subject: "Sale", Content: "<p>Promo</p>"},
				{Category: "campaign", Recipient: "customer@example.com", Subject: "Blast", Content: "<p>Blast</p>"},
				{Category: "broadcast", Recipient: "customer@example.com", Subject: "News", Content: "<p>News</p>"},
				{Category: AdtySystemEmailCategoryOrderNotice, Recipient: "customer@example.com", Subject: "Promo", Content: "<p>List-Unsubscribe: https://example.com/unsub</p>"},
			} {
				_, err := EnqueueAdtySystemEmail(bad)
				assert.ErrorIs(t, err, ErrAdtySystemEmailMarketingRetired)
			}

			// 2. 允许收件人边界：注册验证码不能发给同站已注册邮箱；密码重置/安全/订单通知不能发给无关或跨站邮箱
			_, err := EnqueueAdtySystemEmail(AdtySystemEmailRequest{
				Category:  AdtySystemEmailCategoryVerification,
				SiteID:    0,
				Recipient: "customer@example.com",
				Subject:   "Verification",
				Content:   "<p>Code: 123456</p>",
			})
			assert.ErrorIs(t, err, ErrAdtySystemEmailRecipientForbidden)

			_, err = EnqueueAdtySystemEmail(AdtySystemEmailRequest{
				Category:  AdtySystemEmailCategoryPasswordReset,
				SiteID:    0,
				UserID:    customer.Id,
				Recipient: "stranger@example.com",
				Subject:   "Password Reset",
				Content:   "<p>https://main.example.com/user/reset#token=secretResetToken999</p>",
			})
			assert.ErrorIs(t, err, ErrAdtySystemEmailRecipientForbidden)

			_, err = EnqueueAdtySystemEmail(AdtySystemEmailRequest{
				Category:  AdtySystemEmailCategorySecurityNotice,
				SiteID:    site.Id, // customer belongs to main site (0), not site.Id
				UserID:    customer.Id,
				Recipient: "customer@example.com",
				Subject:   "Security Notice",
				Content:   "<p>Password changed</p>",
			})
			assert.ErrorIs(t, err, ErrAdtySystemEmailRecipientForbidden)

			// 3. 事务邮件发送失败不回滚已提交业务，同时保留 failed 状态、错误信息和可重试记录，且证据中绝不保存真实 token/验证码
			resetToken := "ResetSecretTokenAbc123XYZ"
			resetRow, err := DeliverAdtySystemEmail(AdtySystemEmailRequest{
				EventKey:  "pwd-reset:1",
				Category:  AdtySystemEmailCategoryPasswordReset,
				SiteID:    0,
				UserID:    customer.Id,
				Recipient: "customer@example.com",
				Subject:   "重置密码",
				Content:   "<p>请点击 https://main.example.com/user/reset#email=customer%40example.com&token=" + resetToken + "</p>",
				Now:       baseNow,
			})
			require.Error(t, err, "initial provider error is reported")
			require.NotNil(t, resetRow)
			assert.Equal(t, AdtySystemEmailStatusFailed, resetRow.Status)
			assert.Equal(t, 1, resetRow.Attempts)
			assert.Contains(t, resetRow.LastError, "451")
			assert.Equal(t, "zh-CN", resetRow.Language)
			assert.NotContains(t, resetRow.ContentRedacted, resetToken, "persisted evidence must redact reset token")
			assert.Contains(t, resetRow.ContentRedacted, "token=[REDACTED]")

			// 确认数据库底表列中同样不存在明文 token
			var rawCipher, rawRedacted string
			require.NoError(t, db.Model(&AdtySystemEmailDelivery{}).Where("id = ?", resetRow.Id).Select("body_cipher").Scan(&rawCipher).Error)
			require.NoError(t, db.Model(&AdtySystemEmailDelivery{}).Where("id = ?", resetRow.Id).Select("content_redacted").Scan(&rawRedacted).Error)
			assert.NotEmpty(t, rawCipher)
			assert.NotContains(t, rawCipher, resetToken)
			assert.NotContains(t, rawRedacted, resetToken)

			// 已提交的客户余额/状态不受邮件发送失败影响
			var storedCustomer model.User
			require.NoError(t, db.First(&storedCustomer, customer.Id).Error)
			assert.Equal(t, 500000, storedCustomer.Quota)

			// 4. 覆盖邮箱绑定、安全通知、订单通知（支持 NotificationEmail）及分站注册验证码入队后进程退出（未派发后台协程）恢复
			AdtySystemEmailAsyncDispatch = false
			AdtySendSiteEmailAsync(&site, "Mail Shop Verification", "newcomer@example.com", "<p>Your verification code is <strong>482910</strong></p>")

			bindRow, err := EnqueueAdtySystemEmail(AdtySystemEmailRequest{
				EventKey:       "email-bind:1",
				Category:       AdtySystemEmailCategoryEmailBinding,
				SiteID:         site.Id,
				UserID:         siteCustomer.Id,
				Recipient:      "newbind@example.com",
				AllowedAddress: "newbind@example.com",
				Subject:        "Email Binding Verification",
				Content:        "<p>Binding code: 739201</p>",
				Now:            baseNow,
			})
			require.NoError(t, err)
			assert.Equal(t, "en", bindRow.Language)
			assert.NotContains(t, bindRow.ContentRedacted, "739201")
			assert.Contains(t, bindRow.ContentRedacted, "[REDACTED_CODE]")

			secRow, err := EnqueueAdtySystemEmail(AdtySystemEmailRequest{
				EventKey:       "sec-notice:1",
				Category:       AdtySystemEmailCategorySecurityNotice,
				SiteID:         0,
				UserID:         customer.Id,
				Recipient:      "old-address@example.com",
				AllowedAddress: "old-address@example.com",
				Subject:        "账户安全通知",
				Content:        "<p>您的邮箱已变更，API Key sk-supersecretkey123456 已脱敏</p>",
				Now:            baseNow,
			})
			require.NoError(t, err)
			assert.Equal(t, "zh-CN", secRow.Language)
			assert.NotContains(t, secRow.ContentRedacted, "sk-supersecretkey123456")

			orderRow, err := EnqueueAdtySystemEmail(AdtySystemEmailRequest{
				EventKey:  "order-notice:T20001",
				Category:  AdtySystemEmailCategoryOrderNotice,
				SiteID:    0,
				UserID:    customer.Id,
				Recipient: "billing-notify@example.com",
				Subject:   "订单充值成功通知",
				Content:   "<p>订单 T20001 已入账 10.00 USD</p>",
				Now:       baseNow,
			})
			require.NoError(t, err)
			assert.Equal(t, AdtySystemEmailStatusPending, orderRow.Status)

			// 5. 过期任务处理：一条已过期的注册验证码在重试时标为 expired，绝不发信
			expiredRow, err := EnqueueAdtySystemEmail(AdtySystemEmailRequest{
				EventKey:  "verify-expired:1",
				Category:  AdtySystemEmailCategoryVerification,
				SiteID:    0,
				Recipient: "expired-user@example.com",
				Subject:   "Verification Code",
				Content:   "<p>Code: 918273</p>",
				TTL:       2 * time.Minute,
				Now:       baseNow.Add(-5 * time.Minute),
			})
			require.NoError(t, err)

			// 恢复发送提供方，模拟进程重启后执行 RetryAdtySystemEmails
			failDelivery = false
			processed, err := RetryAdtySystemEmails(baseNow.Add(2 * time.Minute))
			require.NoError(t, err)
			assert.GreaterOrEqual(t, processed, 5)

			// 过期验证码已转为 expired 且清空密文，未向外发送
			var afterExpired AdtySystemEmailDelivery
			require.NoError(t, db.First(&afterExpired, expiredRow.Id).Error)
			assert.Equal(t, AdtySystemEmailStatusExpired, afterExpired.Status)
			assert.Empty(t, afterExpired.BodyCipher)

			// 其余 5 条（密码重置失败重试 + 进程重启前 pending 的验证码/绑定/安全/订单通知）均已成功投递并清空 BodyCipher
			for _, id := range []int64{resetRow.Id, bindRow.Id, secRow.Id, orderRow.Id} {
				var after AdtySystemEmailDelivery
				require.NoError(t, db.First(&after, id).Error)
				assert.Equal(t, AdtySystemEmailStatusSent, after.Status, "delivery id=%d should be sent", id)
				assert.NotZero(t, after.SentAt)
				assert.Empty(t, after.LastError)
				assert.Empty(t, after.BodyCipher)
			}

			var asyncVerifyRow AdtySystemEmailDelivery
			require.NoError(t, db.Where("recipient = ?", "newcomer@example.com").First(&asyncVerifyRow).Error)
			assert.Equal(t, AdtySystemEmailStatusSent, asyncVerifyRow.Status)
			assert.Equal(t, site.Id, asyncVerifyRow.SiteId)
			assert.NotContains(t, asyncVerifyRow.ContentRedacted, "482910")
			assert.Empty(t, asyncVerifyRow.BodyCipher)

			mu.Lock()
			assert.Len(t, delivered, 5, "all 5 unexpired transactional emails must be delivered once and expired email skipped")
			mu.Unlock()
		})
	}
}
