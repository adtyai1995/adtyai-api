package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// auditContentTemplates 将稳定的操作标识 action 映射为英文兜底模板，渲染后写入
// Log.Content（供导出等非本地化消费者使用）。占位符为 ${name}，由该
// action 的 params 填充。本地化展示文案在前端 i18n 模板中维护，本表是语言中立的
// 英文基线——调用方因此无需在每个埋点处手写句子（避免与 params 重复书写同一份值）。
var auditContentTemplates = map[string]string{
	"token.create":               "API token creation",
	"token.update":               "API token configuration update",
	"token.status_update":        "API token status update",
	"token.delete":               "API token deletion",
	"token.delete_batch":         "API token batch deletion",
	"token.key_view":             "API token key access",
	"token.key_view_batch":       "API token batch key access",
	"user.create":                "Created user ${username} (role ${role})",
	"user.update":                "Updated user ${username} (ID: ${id})",
	"user.delete":                "Deleted user ${username} (ID: ${id})",
	"user.account_delete":        "Account deletion",
	"user.manage":                "Performed ${action} on user ${username} (ID: ${id})",
	"user.quota_add":             "Increased user quota by ${quota}",
	"user.quota_subtract":        "Decreased user quota by ${quota}",
	"user.quota_override":        "Overrode user quota from ${from} to ${to}",
	"user.binding_clear":         "Cleared ${bindingType} binding for user ${username}",
	"user.2fa_disable":           "Force-disabled two-factor authentication for the user",
	"user.passkey_register":      "Registered a passkey",
	"access_token.generate":      "Generated an access token",
	"access_token.revoke":        "Revoked an access token",
	"access_token.rename":        "Renamed an access token",
	"access_token.update":        "Changed access token permissions",
	"user.2fa_setup":             "Started two-factor authentication setup",
	"user.2fa_enable":            "Enabled two-factor authentication",
	"user.2fa_disable_self":      "Disabled two-factor authentication",
	"user.2fa_backup_codes":      "Regenerated two-factor backup codes",
	"user.security_verify":       "Completed security verification",
	"user.security_verify_failed": "Security verification failed",
	"user.password_change":       "Account password change",
	"user.binding_start":         "Account binding request",
	"user.binding_bind":          "Account binding",
	"user.binding_unbind":        "Account unlinking",
	"user.email_binding_resend":  "Email confirmation code resend",
	"user.passkey_delete":        "Deleted a passkey",
	"user.reset_passkey":         "Reset the user passkey",
	"admin.user.password_reset":  "Reset password for user ${username} (ID: ${id})",
	"adty.site.customer.password_reset": "Reset password for site customer ${username} (ID: ${customer_id})",
	"option.update":                     "Updated system setting ${key}",

	"option.passkey_domains":           "Updated Passkey domains: removed ${domains}; affected ${known}; unknown ${unknown}",
	"option.passkey_domains_confirmed": "Confirmed removal of Passkey domains: ${domains}; affected ${known}; unknown ${unknown}",
	"option.passkey_domains_blocked":   "Passkey domain change blocked: ${domains}; affected ${known}; unknown ${unknown}",
	"option.passkey_domains_failed":    "Passkey domain update failed",

	"channel.create":                       "Created channel ${name} (type ${type}, count ${count})",
	"channel.update":                       "Updated channel ${name} (ID: ${id})",
	"channel.status_update":                "Updated channel status (ID: ${id})",
	"channel.status_update_batch":          "Batch updated channel status (${count}/${total} changed)",
	"channel.delete":                       "Deleted channel ${name} (ID: ${id})",
	"channel.delete_batch":                 "Batch deleted ${count} channels",
	"channel.delete_disabled":              "Deleted all disabled channels (${count})",
	"channel.key_view":                     "Viewed channel key ${name} (ID: ${id})",
	"channel.tag_disable":                  "Disabled channels with tag ${tag}",
	"channel.tag_enable":                   "Enabled channels with tag ${tag}",
	"channel.tag_edit":                     "Edited channels with tag ${tag}",
	"channel.tag_batch_set":                "Batch set tag for ${count} channels",
	"channel.copy":                         "Copied channel (source ID: ${sourceId}) to ${name} (new ID: ${id})",
	"channel.multi_key_manage":             "Multi-key management ${action} on channel (ID: ${id})",
	"channel.fetch_models_saved":           "Saved fetched models for channel ${channel_id}",
	"channel.upstream_apply":               "Applied upstream model changes to channel (ID: ${id})",
	"channel.upstream_apply_all":           "Applied upstream model changes to ${count} channels",
	"channel.upstream_detect_all":          "Detected upstream model updates for all channels",
	"adty.group.create":                    "Created group ${id}",
	"adty.group.update":                    "Updated group ${id}",
	"adty.group.migrate":                   "Migrated group ${id} to ${to}",
	"adty.group.delete":                    "Deleted group ${id}",
	"adty.admin_permissions.update":        "Changed admin permissions of user ${id}",
	"adty.payment_orphan.credit":           "Credited payment event ${id}",
	"adty.payment_orphan.resolve":          "Resolved payment event ${id} as ${status}",
	"adty.topup.manual_complete":           "Completed top-up ${trade_no} manually",
	"adty.topup.query":                     "Queried top-up ${trade_no}",
	"adty.topup.refund":                    "Refunded top-up ${trade_no}",
	"adty.topup.refund.apply":              "Applied manual refund for top-up ${trade_no}",
	"adty.topup.refund.approve":            "Approved manual refund for top-up ${trade_no}",
	"adty.topup.refund.reclaim":            "Reclaimed principal for top-up refund ${trade_no}",
	"adty.topup.refund.bonus":              "Planned bonus adjustment for top-up refund ${trade_no}",
	"adty.topup.refund.commission":         "Planned commission clawback for top-up refund ${trade_no}",
	"adty.topup.refund.payout":             "Recorded payout for top-up refund ${trade_no}",
	"adty.topup.refund.complete":           "Completed manual refund for top-up ${trade_no}",
	"adty.topup.refund.terminate":          "Terminated manual refund for top-up ${trade_no}",
	"adty.topup.method_bonus.update":       "Updated recharge bonus rates",
	"adty.topup.site_bonus_margin_fill.update": "Updated subsite bonus margin fallback settings",
	"adty.topup.site_method_bonus.update":  "Updated subsite recharge bonus rates",
	"adty.root_gift.grant":                 "Granted expiring trial gift",
	"adty.gift.reserve":                    "Reserved gift guarantee",
	"adty.gift.fulfill":                    "Fulfilled pending gift",
	"adty.gift.release":                    "Released gift guarantee",
	"adty.gift.spend":                      "Spent gift guarantee",
	"adty.gift.spend_refund":               "Refunded spent gift guarantee",
	"adty.gift.fulfillment.review":         "Reviewed gift fulfillment ${trade_no} (${action})",
	"adty.team.create":                     "Created team",
	"adty.team.close":                      "Closed team",
	"adty.team.discount.update":            "Updated team ${team_id} discount from ${before} to ${after}",
	"adty.team.member.create":              "Created team member",
	"adty.team.member.disable":             "Disabled team member",
	"adty.team.member.enable":              "Enabled team member",
	"adty.team.member.delete":              "Deleted team member",
	"adty.team.member.password_reset":      "Reset team member password",
	"adty.team.member.leave":               "Team member left team",
	"adty.team.invite.create":              "Created team invite",
	"adty.team.invite.accept":              "Accepted team invite",
	"adty.team.invite.revoke":              "Revoked team invite",
	"adty.team.key.bind":                   "Bound team API key",
	"adty.team.key.revoke":                 "Revoked team API key",
	"adty.team.registration.create":        "Created team registration link",
	"adty.team.registration.complete":      "Completed team registration",
	"adty.team.registration.revoke":        "Revoked team registration link",
	"adty.epay_channel.create":             "Created payment channel ${name} (ID: ${id})",
	"adty.epay_channel.update":             "Updated payment channel ${name} (ID: ${id})",
	"adty.epay_channel.delete":             "Deleted payment channel ${id}",
	"adty.epay_channel.revoke":             "Revoked payment channel ${id} credentials",
	"adty.epay_channel.status":             "Set payment channel ${id} to ${status}",
	"adty.epay_channel.test":               "Tested payment channel ${id}",
	"adty.epay_channel.maintenance":        "Payment channel ${id} moved to maintenance",
	"adty.referral.settings":               "Updated referral settings",
	"adty.referral.affiliate_update":       "Updated an affiliate (${action})",
	"adty.referral.withdrawal_review":      "Reviewed withdrawal ${id}",
	"adty.referral.withdrawal_pay":         "Marked withdrawal ${id} as paid",
	"adty.user.limits":                     "Set request limits: RPM ${rpm}, TPM ${tpm}, concurrency ${concurrency}",
	"adty.user.quota_adjust":               "Adjusted user quota manually",
	"adty.user.password_reset.challenge":   "Requested password reset verification",
	"adty.site.open":                       "Opened reseller site",
	"adty.site.review":                     "Reviewed reseller site application",
	"adty.site.approve":                    "Approved reseller site application",
	"adty.site.reject":                     "Rejected reseller site application",
	"adty.site.adjust":                     "Adjusted reseller site",
	"adty.site.status":                     "Updated reseller site status",
	"adty.site.paused_repair":              "Repaired paused reseller site",
	"adty.site.close_request":              "Requested reseller site closure",
	"adty.site.close_schedule":             "Scheduled reseller site closure",
	"adty.site.close_reschedule":           "Rescheduled reseller site closure",
	"adty.site.close":                      "Closed reseller site",
	"adty.site.archive_hide":               "Hid archived reseller site",
	"adty.site.delete":                     "Deleted reseller site",
	"adty.site.settings":                   "Updated reseller settings",
	"adty.site.tiers":                      "Updated site tiers",
	"adty.site.tier":                       "Updated site tier",
	"adty.site.domain_disable":             "Disabled site custom domain ${domain_id}",
	"adty.site.sender_domain_disable":      "Disabled site sender domain ${site_id}",
	"adty.site.legal_update":               "Updated site legal documents",
	"adty.site.mail_config_update":         "Updated site email configuration",
	"adty.site.close_mail_retry":           "Retried site closure migration email",
	"adty.site.close_mail_delivered":       "Marked site closure migration email as delivered",
	"site.close_mail.retry":                "Retried site closure migration email batch",
	"adty.site.customer_review":            "Reviewed flagged site customer",
	"adty.site.earning_review":             "Reviewed site earning event",
	"adty.site.settlement_resume":          "Resumed site settlement",
	"adty.site.report":                     "Handled site report",
	"adty.site.report_resolved":            "Resolved site report",
	"adty.site.report_dismissed":           "Dismissed site report",
	"adty.site.notice_takedown":            "Took down reseller site notice",
	"adty.site.customer_balance_credit":    "Credited site customer balance",
	"adty.site.team_balance_credit":        "Credited site team balance",
	"adty.site.tier_principal_adjust":      "Adjusted site tier principal",
	"adty.site.customer_relation_recovery_needed": "Site customer relation recovery required",
	"adty.site.order.refund.apply":         "Applied manual refund for site order ${trade_no}",
	"adty.site.order.refund.approve":       "Approved manual refund for site order ${trade_no}",
	"adty.site.order.refund.reclaim":       "Reclaimed principal for site order refund ${trade_no}",
	"adty.site.order.refund.bonus":         "Planned bonus adjustment for site order refund ${trade_no}",
	"adty.site.order.refund.commission":    "Planned commission clawback for site order refund ${trade_no}",
	"adty.site.order.refund.payout":        "Recorded payout for site order refund ${trade_no}",
	"adty.site.order.refund.complete":      "Completed manual refund for site order ${trade_no}",
	"adty.site.order.refund.terminate":     "Terminated manual refund for site order ${trade_no}",
	"margin.root_credit":                   "Credited site margin by Root",
	"adty.margin.margin_topup":             "Topped up site margin",
	"adty.margin.margin_cycle_open":        "Opened site margin cycle",
	"adty.margin.earning_to_margin":        "Transferred earnings to margin",
	"adty.margin.commission_freeze":        "Froze margin for invite commission",
	"adty.margin.commission_release":       "Released margin for invite commission",
	"adty.margin.commission_deduct":               "Deducted margin for invite commission",
	"adty.margin.commission_closed_payout_deduct": "Deducted margin for closed-site invite commission payout",
	"adty.margin.commission_clawback_return":      "Returned clawed-back invite commission to margin",
	"adty.anomaly_disposition.review":      "Reviewed fund anomaly disposition",
	"adty.site_withdraw.rules.update":      "Updated site withdrawal rules",
	"adty.site_withdraw.create":            "Created site withdrawal request",
	"adty.site_withdraw.paying":            "Marked site withdrawal as paying",
	"adty.site_withdraw.recall":            "Recalled site withdrawal payout",
	"adty.site_withdraw.failed":            "Marked site withdrawal payout as failed",
	"adty.site_withdraw.paid":              "Marked site withdrawal as paid",
	"adty.site_withdraw.reject":            "Rejected site withdrawal request",
	"adty.site_withdraw.cancel":            "Cancelled site withdrawal request",
	"adty.site_invite_withdrawal.approve":  "Approved site invite commission withdrawal",
	"adty.site_invite_withdrawal.reject":   "Rejected site invite commission withdrawal",
	"adty.site_invite_withdrawal.failed":   "Marked site invite commission withdrawal as failed",
	"adty.site_invite_withdrawal.paid":     "Marked site invite commission withdrawal as paid",
	"adty.voucher.issue":                   "Issued recharge voucher",
	"adty.voucher.redeem":                  "Redeemed recharge voucher",
	"adty.voucher.freeze":                  "Froze recharge voucher",
	"adty.voucher.cancel":                  "Cancelled recharge voucher",
	"staff.create":                         "Created site staff account",
	"staff.update":                         "Updated site staff account",
	"staff.password":                       "Reset site staff password",
	"staff.delete":                         "Deleted site staff account",
	"staff.customer.disable":               "Disabled site customer by staff",
	"adty.staff.customers.read":            "Viewed site customer list as staff",
	"adty.staff.customer.read":             "Viewed site customer details as staff",
	"adty.staff.topups.read":               "Viewed site customer top-ups as staff",
	"adty.staff.orders.read":               "Viewed site customer orders as staff",
	"adty.staff.usage.read":                "Viewed site customer usage logs as staff",
	"adty.telegram.save":                   "Updated Telegram bot (${purpose})",
	"adty.telegram.unbind":                 "Unbound Telegram notification (${purpose})",
	"adty.translation.save":                "Updated translation settings (${mode})",
	"adty.translation.retry":               "Retried translation job ${object_key}",
	"adty.translation.complete":            "Completed translation job ${object_key}",
	"adty.translation.edit":                "Edited translation entry ${object_key}",
	"adty.translation.glossary.save":       "Saved translation glossary entry",
	"adty.translation.glossary.delete":     "Deleted translation glossary entry",
	"adty.ticket.create":                   "Created support ticket",
	"adty.ticket.status":                   "Updated support ticket status",
	"adty.ticket.reply":                    "Replied to support ticket",
	"adty.ticket.image.view":               "Viewed support ticket image",
	"adty.ticket.image.delete":             "Deleted support ticket image",
	"adty.notification.create":             "Created notification publication",
	"adty.notification.update":             "Updated notification publication",
	"adty.notification.withdraw":           "Withdrew notification publication",
	"adty.notification.hide":               "Hid notification",
	"adty.notice_template.save":            "Saved notice template",
	"adty.todo.transition":                 "Updated todo item status",
	"adty.todo.complete":                   "Completed todo item",
	"data_retention.update":                "Updated data retention policy",
	"adty.payment_scenes.update":           "Updated payment scenes",
	"adty.ticket.image_policy.update":      "Updated ticket image policy",
	"vendor.metadata.save":                 "Saved vendor metadata ${name} (ID: ${vendor_id})",
	"vendor.metadata.delete":               "Deleted vendor metadata (ID: ${vendor_id})",
	"vendor.assign":                        "Assigned models to vendor ${target_vendor_id}",
	"vendor.merge":                         "Merged vendors into vendor ${target_vendor_id}",
	"model.delete":                         "Deleted a model",
	"model.delete_batch":                   "Batch deleted models",
	"model.metadata.sync":                  "Synced model metadata",
	"model.pricing.update":                 "Updated model pricing",
	"system_task.cancel":                   "Cancelled system task ${task_id}",
}

const (
	auditDurablePersistedContextKey = "adty_audit_durable_persisted"
	auditPreparedEventIDsContextKey = "adty_audit_prepared_event_ids"
)

func rememberPreparedAuditEvent(c *gin.Context, eventID string) {
	if c == nil || strings.TrimSpace(eventID) == "" {
		return
	}
	existing, _ := common.GetContextKeyType[[]string](c, auditPreparedEventIDsContextKey)
	existing = append(existing, eventID)
	common.SetContextKey(c, auditPreparedEventIDsContextKey, existing)
}

func hasDurableAuditIntent(eventID string) bool {
	if strings.TrimSpace(eventID) == "" || model.DB == nil {
		return false
	}
	var count int64
	if err := model.DB.Model(&model.AuditIntent{}).Where("event_id = ?", eventID).Limit(1).Count(&count).Error; err != nil {
		return false
	}
	return count > 0
}

func persistAndDeliverAuditEvent(c *gin.Context, event model.AuditLog) bool {
	if strings.TrimSpace(event.EventId) == "" || model.DB == nil {
		return false
	}
	intentEvent, omitFinancialRef := operationIntentWithoutDuplicateFinancialRef(event)
	if !hasDurableAuditIntent(event.EventId) {
		if err := model.AppendAuditIntentTx(model.DB, intentEvent); err != nil {
			return false
		}
	}
	if c != nil {
		common.SetContextKey(c, auditDurablePersistedContextKey, true)
	}
	markAuditLogged(c)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if omitFinancialRef {
		// The in-transaction financial intent already identifies that object.
		// Keep the link on the operator log, and do not replay the stripped
		// intent payload over that snapshot.
		if err := model.WritePreparedAuditLog(ctx, event); err != nil {
			return false
		}
		if err := model.AcknowledgeAuditIntent(ctx, event.EventId); err != nil {
			return false
		}
		return true
	}
	_ = model.DeliverAuditIntent(ctx, event.EventId)
	return true
}

// operationIntentWithoutDuplicateFinancialRef copies an operation audit and
// removes a financial_adjustment approval reference from the intent payload.
// The returned bool is true only when the copy differs from the operator snapshot.
func operationIntentWithoutDuplicateFinancialRef(event model.AuditLog) (model.AuditLog, bool) {
	intent := event
	omitted := false
	if strings.Contains(intent.ApprovalRef, "financial_adjustment:") {
		intent.ApprovalRef = ""
		omitted = true
	}
	if intent.Other.Op == nil || intent.Other.Op.Params == nil {
		return intent, omitted
	}
	raw, ok := intent.Other.Op.Params["approval_ref"]
	if !ok || !strings.Contains(fmt.Sprint(raw), "financial_adjustment:") {
		return intent, omitted
	}
	op := *intent.Other.Op
	params := make(model.AuditFields, len(op.Params))
	for key, value := range op.Params {
		if key == "approval_ref" {
			continue
		}
		params[key] = value
	}
	op.Params = params
	intent.Other.Op = &op
	return intent, true
}

func recordPasskeyDomainAudit(c *gin.Context, change *model.PasskeyDomainChange, confirmed bool, err error) {
	if c == nil {
		return
	}
	confirmed = confirmed && err == nil && change != nil && len(change.RemovedRPIDs) > 0
	params := map[string]any{"success": err == nil, "confirmed": confirmed}
	var before, after any
	if change != nil {
		params["domains"] = strings.Join(change.RemovedRPIDs, ", ")
		params["removed_rp_ids"] = change.RemovedRPIDs
		params["known"] = change.AffectedCredentials
		params["unknown"] = change.UnknownCredentials
		params["previous_rp_id"] = change.PreviousRPID
		params["effective_rp_id"] = change.EffectiveRPID
		before = change.PreviousRPID
		after = change.EffectiveRPID
	}
	action := "option.passkey_domains"
	if errors.Is(err, model.ErrPasskeyDomainRemovalConfirmation) {
		action = "option.passkey_domains_blocked"
	} else if err != nil {
		action = "option.passkey_domains_failed"
	} else if confirmed && change != nil && len(change.RemovedRPIDs) > 0 {
		action = "option.passkey_domains_confirmed"
	}
	status := http.StatusOK
	if c.Writer != nil && c.Writer.Status() > 0 {
		status = c.Writer.Status()
	}
	var auditInfo *model.AuditRequestInfo
	if c.Request != nil {
		auditInfo = &model.AuditRequestInfo{
			Method: c.Request.Method, Route: c.FullPath(), Path: c.FullPath(),
			Status: status, Success: err == nil,
		}
	}
	siteID := service.AdtyRequestSiteID(c)
	if inferred := model.InferOfficialAuditSiteID(c, params); inferred != 0 || params["site_id"] != nil {
		siteID = inferred
	}
	objectType, objectID := model.InferOfficialAuditObject(action, params, strconv.Itoa(c.GetInt("id")), action)
	entry, prepErr := model.PrepareOfficialAuditLog(c, model.OfficialAuditWrite{
		UserId: c.GetInt("id"), Username: c.GetString("username"), ActorRole: c.GetInt("role"),
		SiteID: model.OfficialAuditSiteIDPointer(siteID), ObjectType: objectType, ObjectID: objectID,
		Action: action, Category: model.AuditCategoryOperation, Content: auditContentEN(action, params),
		Before: before, After: after, Reason: model.InferOfficialAuditReason(params),
		ApprovalRef: model.InferOfficialAuditApprovalRef(params), Result: model.OfficialAuditResult(err == nil),
		Success: err == nil, Status: status,
		Other: model.AuditOther{Op: &model.AuditOperation{Action: action, Params: params}, AdminInfo: auditOperatorInfo(c), AuditInfo: auditInfo},
	})
	if prepErr != nil {
		return
	}
	rememberPreparedAuditEvent(c, entry.EventId)
	persistAndDeliverAuditEvent(c, entry)
}

// auditContentEN 按 action 模板渲染英文兜底文本；未登记的 action 退回 action 本身。
func auditContentEN(action string, params map[string]any) string {
	tmpl, ok := auditContentTemplates[action]
	if !ok {
		return action
	}
	return os.Expand(tmpl, func(key string) string {
		if v, ok := params[key]; ok {
			return fmt.Sprintf("%v", v)
		}
		return ""
	})
}

// auditOperatorInfo 从上下文构建操作者身份信息（管理员 id/用户名/角色）。
func auditOperatorInfo(c *gin.Context) *model.AuditAdminInfo {
	return &model.AuditAdminInfo{
		AdminID:       c.GetInt("id"),
		AdminUsername: c.GetString("username"),
		AdminRole:     c.GetInt("role"),
		AuthMethod:    auditAuthMethod(c),
	}
}

func auditAuthMethod(c *gin.Context) string {
	if c.GetBool("use_access_token") {
		return "access_token"
	}
	return "session"
}

// markAuditLogged 标记当前请求已在 handler 内完成持久审计日志落盘，
// 使鉴权链路中的审计兜底（finishAdminAudit）跳过兜底记录，避免重复。
// 未证明已写入 AuditIntent 持久层时不会设置 ContextKeyAuditLogged。
func markAuditLogged(c *gin.Context) {
	if c == nil {
		return
	}
	if !common.GetContextKeyBool(c, auditDurablePersistedContextKey) {
		ids, _ := common.GetContextKeyType[[]string](c, auditPreparedEventIDsContextKey)
		if len(ids) == 0 || model.DB == nil {
			return
		}
		for _, id := range ids {
			if !hasDurableAuditIntent(id) {
				return
			}
		}
		common.SetContextKey(c, auditDurablePersistedContextKey, true)
	}
	common.SetContextKey(c, constant.ContextKeyAuditLogged, true)
}

// markIdempotentReplayNoAudit suppresses the admin-route generic fallback audit when
// the handler verified an already-audited idempotent replay that performed no new write (R5-042).
func markIdempotentReplayNoAudit(c *gin.Context) {
	if c == nil {
		return
	}
	common.SetContextKey(c, constant.ContextKeyAuditLogged, true)
}

// recordManageAudit 记录一条由操作者本人归属的管理/高危审计日志（资源类操作：
// 渠道 / 系统设置 / 兑换码等）。content 由 action+params 自动渲染。
func recordManageAudit(c *gin.Context, action string, params map[string]any) bool {
	return recordManageAuditFor(c, c.GetInt("id"), action, params)
}

// recordDurableOperationAudit persists one official operation audit and only
// then suppresses the admin-route fallback. A direct audit-log insert does not
// prove durability, so it must not be used to skip that fallback.
func recordDurableOperationAudit(c *gin.Context, action, content string, params map[string]any, success bool) {
	if c == nil {
		return
	}
	if params == nil {
		params = map[string]any{}
	}
	status := http.StatusOK
	if c.Writer != nil && c.Writer.Status() > 0 {
		status = c.Writer.Status()
	}
	username := c.GetString("username")
	if username == "" && c.GetInt("id") > 0 {
		username, _ = model.GetUsernameById(c.GetInt("id"), false)
	}
	siteID := service.AdtyRequestSiteID(c)
	if inferred := model.InferOfficialAuditSiteID(c, params); inferred != 0 || params["site_id"] != nil {
		siteID = inferred
	}
	objectType, objectID := model.InferOfficialAuditObject(action, params, strconv.Itoa(c.GetInt("id")), action)
	before, after := model.InferOfficialAuditBeforeAfter(params)
	entry, err := model.PrepareOfficialAuditLog(c, model.OfficialAuditWrite{
		UserId: c.GetInt("id"), Username: username, ActorRole: c.GetInt("role"),
		SiteID: model.OfficialAuditSiteIDPointer(siteID), ObjectType: objectType, ObjectID: objectID,
		Action: action, Category: model.AuditCategoryOperation, Content: content,
		Before: before, After: after, Reason: model.InferOfficialAuditReason(params),
		ApprovalRef: model.InferOfficialAuditApprovalRef(params), Result: model.OfficialAuditResult(success),
		Success: success, Status: status, Ip: c.ClientIP(),
		Other: model.AuditOther{
			Op:        &model.AuditOperation{Action: action, Params: params},
			AdminInfo: auditOperatorInfo(c),
			AuditInfo: &model.AuditRequestInfo{
				Method: c.Request.Method, Route: c.FullPath(), Path: c.FullPath(),
				Status: status, Success: success,
			},
		},
	})
	if err != nil {
		return
	}
	rememberPreparedAuditEvent(c, entry.EventId)
	persistAndDeliverAuditEvent(c, entry)
}

// recordManageAuditFor 记录一条管理审计日志，日志归属于操作者；targetUserId
// 只表示被操作用户，用于在结构化参数中保留目标上下文。
func recordManageAuditFor(c *gin.Context, targetUserId int, action string, params map[string]any) bool {
	if params == nil {
		params = map[string]any{}
	}
	operatorUserId := 0
	if c != nil {
		operatorUserId = c.GetInt("id")
	}
	if _, ok := params["target_user_id"]; !ok && targetUserId > 0 && targetUserId != operatorUserId {
		params["target_user_id"] = targetUserId
	}
	entry := prepareManageAudit(c, targetUserId, action, params)
	return persistAndDeliverAuditEvent(c, entry)
}

// prepareManageAudit freezes safe metadata before entering the business
// transaction. No request body, credentials, configuration value or live-user
// lookup is needed when the event is eventually delivered.
func prepareManageAudit(c *gin.Context, targetUserID int, action string, params map[string]any) model.AuditLog {
	if params == nil {
		params = map[string]any{}
	}
	if targetUserID > 0 && targetUserID != c.GetInt("id") {
		params["target_user_id"] = targetUserID
	}
	siteID := service.AdtyRequestSiteID(c)
	if inferred := model.InferOfficialAuditSiteID(c, params); inferred != 0 || params["site_id"] != nil {
		siteID = inferred
	}
	objectType, objectID := model.InferOfficialAuditObject(action, params, strconv.Itoa(targetUserID), strconv.Itoa(c.GetInt("id")), action)
	before, after := model.InferOfficialAuditBeforeAfter(params)
	entry, err := model.PrepareOfficialAuditLog(c, model.OfficialAuditWrite{
		UserId: c.GetInt("id"), Username: c.GetString("username"), ActorRole: c.GetInt("role"),
		SiteID: model.OfficialAuditSiteIDPointer(siteID), ObjectType: objectType, ObjectID: objectID,
		Action: action, Category: model.AuditCategoryOperation, Content: auditContentEN(action, params),
		Before: before, After: after, Reason: model.InferOfficialAuditReason(params),
		ApprovalRef: model.InferOfficialAuditApprovalRef(params), Result: model.AuditResultSuccess,
		Success: true, Status: http.StatusOK,
		Other: model.AuditOther{Op: &model.AuditOperation{Action: action, Params: params}, AdminInfo: auditOperatorInfo(c)},
	})
	if err != nil {
		return model.AuditLog{}
	}
	rememberPreparedAuditEvent(c, entry.EventId)
	return entry
}

// prepareManageAuditChange freezes an official G.13.2 record with explicit
// before/after business values, reason, and optional approval association.
func prepareManageAuditChange(c *gin.Context, targetUserID int, action string, params map[string]any, before, after any, reason, approvalRef string) model.AuditLog {
	if params == nil {
		params = map[string]any{}
	}
	if targetUserID > 0 && targetUserID != c.GetInt("id") {
		params["target_user_id"] = targetUserID
	}
	if strings.TrimSpace(reason) != "" && params["reason"] == nil {
		params["reason"] = reason
	}
	if strings.TrimSpace(approvalRef) != "" && params["approval_ref"] == nil {
		params["approval_ref"] = approvalRef
	}
	siteID := service.AdtyRequestSiteID(c)
	if inferred := model.InferOfficialAuditSiteID(c, params); inferred != 0 || params["site_id"] != nil {
		siteID = inferred
	}
	objectType, objectID := model.InferOfficialAuditObject(action, params, strconv.Itoa(targetUserID), strconv.Itoa(c.GetInt("id")), action)
	if before == nil && after == nil {
		before, after = model.InferOfficialAuditBeforeAfter(params)
	}
	if strings.TrimSpace(reason) == "" {
		reason = model.InferOfficialAuditReason(params)
	}
	if strings.TrimSpace(approvalRef) == "" {
		approvalRef = model.InferOfficialAuditApprovalRef(params)
	}
	entry, err := model.PrepareOfficialAuditLog(c, model.OfficialAuditWrite{
		UserId: c.GetInt("id"), Username: c.GetString("username"), ActorRole: c.GetInt("role"),
		SiteID: model.OfficialAuditSiteIDPointer(siteID), ObjectType: objectType, ObjectID: objectID,
		Action: action, Category: model.AuditCategoryOperation, Content: auditContentEN(action, params),
		Before: before, After: after, Reason: reason, ApprovalRef: approvalRef,
		Result: model.AuditResultSuccess, Success: true, Status: http.StatusOK,
		Other: model.AuditOther{Op: &model.AuditOperation{Action: action, Params: params}, AdminInfo: auditOperatorInfo(c)},
	})
	if err != nil {
		return model.AuditLog{}
	}
	rememberPreparedAuditEvent(c, entry.EventId)
	return entry
}

func deliverCommittedAudits(c *gin.Context, events []model.AuditLog) {
	if len(events) == 0 || model.DB == nil {
		return
	}
	allPersisted := true
	for _, event := range events {
		if strings.TrimSpace(event.EventId) == "" {
			allPersisted = false
			continue
		}
		if !hasDurableAuditIntent(event.EventId) {
			allPersisted = false
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = model.DeliverAuditIntent(ctx, event.EventId)
		cancel()
	}
	if allPersisted {
		if c != nil {
			common.SetContextKey(c, auditDurablePersistedContextKey, true)
		}
		markAuditLogged(c)
	}
}

// recordUserSecurityAudit 记录普通用户自己的安全敏感操作（如 passkey 绑定/解绑）。
// 这类日志没有管理员操作者，不写 admin_info；同时不依赖 AdminAuth/RootAuth 的兜底。
func recordUserSecurityAudit(c *gin.Context, userId int, action string, params map[string]any) {
	if c == nil {
		return
	}
	if params == nil {
		params = map[string]any{}
	}
	if code := c.GetString("security_error_code"); code != "" {
		params["code"] = code
	}
	if c.GetBool("use_access_token") {
		params["token_ref"] = c.GetString("access_token_ref")
	}
	success := true
	var auditInfo *model.AuditRequestInfo
	status := http.StatusOK
	if c.Writer != nil && c.Writer.Status() > 0 {
		status = c.Writer.Status()
	}
	if s, ok := params["success"].(bool); ok {
		success = s
		if c.Request != nil {
			auditInfo = &model.AuditRequestInfo{
				Method: c.Request.Method, Route: c.FullPath(), Path: c.FullPath(),
				Status: status, Success: success,
			}
		}
	}
	username := c.GetString("username")
	if username == "" && userId > 0 {
		username, _ = model.GetUsernameById(userId, false)
	}
	siteID := service.AdtyRequestSiteID(c)
	if inferred := model.InferOfficialAuditSiteID(c, params); inferred != 0 || params["site_id"] != nil {
		siteID = inferred
	}
	objectType, objectID := model.InferOfficialAuditObject(action, params, strconv.Itoa(userId), action)
	before, after := model.InferOfficialAuditBeforeAfter(params)
	entry, err := model.PrepareOfficialAuditLog(c, model.OfficialAuditWrite{
		UserId: userId, Username: username, ActorRole: c.GetInt("role"),
		SiteID: model.OfficialAuditSiteIDPointer(siteID), ObjectType: objectType, ObjectID: objectID,
		Action: action, Category: model.AuditCategorySecurity, Content: auditContentEN(action, params),
		Before: before, After: after, Reason: model.InferOfficialAuditReason(params),
		ApprovalRef: model.InferOfficialAuditApprovalRef(params), Result: model.OfficialAuditResult(success),
		Success: success, Status: status, Ip: c.ClientIP(),
		Other: model.AuditOther{
			Op:        &model.AuditOperation{Action: action, Params: params},
			AuditInfo: auditInfo,
		},
	})
	if err != nil {
		return
	}
	rememberPreparedAuditEvent(c, entry.EventId)
	persistAndDeliverAuditEvent(c, entry)
}

func tokenAuditParams(c *gin.Context) model.AuditFields {
	params, ok := common.GetContextKeyType[model.AuditFields](c, constant.ContextKeyTokenAuditParams)
	if !ok {
		params = model.AuditFields{}
		common.SetContextKey(c, constant.ContextKeyTokenAuditParams, params)
	}
	return params
}

func tokenBatchAuditParams(c *gin.Context, ids []int) model.AuditFields {
	params := tokenAuditParams(c)
	params["total"] = len(ids)
	// Bound audit payloads without changing the batch operation's limits.
	params["requested_ids"] = append([]int{}, ids[:min(len(ids), 100)]...)
	if len(ids) > 100 {
		params["requested_ids_truncated"] = true
	}
	return params
}
