package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	dataRetentionCleanupBatchSize  = 100
	dataRetentionSchedulerInterval = 24 * time.Hour
	secondsPerDay                  = int64(24 * 60 * 60)
	maxDataRetentionDays           = int(math.MaxInt64 / int64(24*time.Hour))

	dataRetentionSkipUnconfigured = "unconfigured; no automatic deletion"
	dataRetentionSkipNotification = "permanent retention; automatic cleanup must not delete notification history"
	dataRetentionSkipOrders       = "policy: orders and payment events are never automatically deleted, including after a retention period expires or an account is cancelled"
	dataRetentionSkipFinancial    = "policy: financial ledgers are never automatically deleted, including after a retention period expires or an account is cancelled"
	dataRetentionSkipArtifacts    = "policy: task artifacts are kept by default, as in official NewAPI; they are never automatically deleted after a retention period expires or an account is cancelled"
	dataRetentionSkipDeidentify   = "de-identification is not implemented for this category; no automatic deletion was performed"
)

// DataRetentionCategoryView is the Root admin representation of one category.
type DataRetentionCategoryView struct {
	Category         string   `json:"category"`
	Title            string   `json:"title"`
	AutoCleanable    bool     `json:"auto_cleanable"`
	Configured       bool     `json:"configured"`
	RetentionDays    *int     `json:"retention_days"`
	Action           string   `json:"action"`
	MinRetentionDays *int     `json:"min_retention_days,omitempty"`
	AllowedActions   []string `json:"allowed_actions"`
	DefaultBehavior  string   `json:"default_behavior"`
	Notes            string   `json:"notes"`
}

type DataRetentionPolicyInput struct {
	Category      string `json:"category"`
	RetentionDays *int   `json:"retention_days"`
	Action        string `json:"action"`
	Clear         bool   `json:"clear"`
}

type DataRetentionRunView struct {
	Id           int64                            `json:"id"`
	StartedAt    int64                            `json:"started_at"`
	FinishedAt   int64                            `json:"finished_at"`
	Trigger      string                           `json:"trigger"`
	Scope        string                           `json:"scope"`
	SystemTaskID string                           `json:"system_task_id,omitempty"`
	Items        []model.AdtyDataRetentionRunItem `json:"items"`
}

type DataRetentionPayload struct {
	Trigger string `json:"trigger"`
}

type DataRetentionResult struct {
	RunID int64 `json:"run_id"`
}

type dataRetentionCategorySpec struct {
	Category        string
	Title           string
	AutoCleanable   bool
	AllowedActions  []string
	DefaultBehavior string
	Notes           string
	MinDays         int
	SkipConfigured  string
}

func dataRetentionCatalog() []dataRetentionCategorySpec {
	return []dataRetentionCategorySpec{
		{
			Category:        model.DataRetentionCategoryUserAccountProfile,
			Title:           "User accounts and profile data",
			AutoCleanable:   true,
			AllowedActions:  []string{model.DataRetentionActionDelete, model.DataRetentionActionDeidentify},
			DefaultBehavior: "no_automatic_deletion",
			Notes:           "Self-cancel immediately clears that account's population and personal-profile fields; the row stays listable as cancelled. When Root sets a period, later runs may idempotently clear leftover PII only on already-cancelled accounts. The task never deletes the account row, and never wipes orders, ledgers, audit, billing facts, or task artifacts because of cancellation.",
		},
		{
			Category:        model.DataRetentionCategoryLoginSecurity,
			Title:           "Login and security records",
			AutoCleanable:   true,
			AllowedActions:  []string{model.DataRetentionActionDelete},
			DefaultBehavior: "no_automatic_deletion",
			Notes:           "When Root sets a period, the official task may delete expired or revoked sessions, expired access tokens, login logs, and login/security audit rows older than that period. The USER_SESSION_REVOKED_RETENTION_DAYS environment setting remains an operational default for the existing session cleaner and is not treated as a signed Root period.",
		},
		{
			Category:        model.DataRetentionCategoryAPIUsageLogs,
			Title:           "API usage logs and records",
			AutoCleanable:   true,
			AllowedActions:  []string{model.DataRetentionActionDelete},
			DefaultBehavior: "no_automatic_deletion",
			Notes:           "Unconfigured usage logs are not auto-deleted. A configured period still honors a 90-day safety floor. Top-up, refund, adjustment, and login rows are not removed by usage cleanup.",
			MinDays:         90,
		},
		{
			Category:        model.DataRetentionCategoryOrdersPayments,
			Title:           "Orders and payment events",
			AutoCleanable:   true,
			AllowedActions:  []string{model.DataRetentionActionDelete},
			DefaultBehavior: "no_automatic_deletion",
			Notes:           "Listed for Root configuration. Automatic cleanup never deletes orders or payment events, including after a configured period expires or an account is cancelled. Changing a policy does not delete history by itself.",
			SkipConfigured:  dataRetentionSkipOrders,
		},
		{
			Category:        model.DataRetentionCategoryFinancialLedgers,
			Title:           "Financial ledgers, wallets, margins, earnings, commissions, and refunds",
			AutoCleanable:   true,
			AllowedActions:  []string{model.DataRetentionActionDelete},
			DefaultBehavior: "no_automatic_deletion",
			Notes:           "Listed for Root configuration. Automatic cleanup never deletes financial ledgers, including after a configured period expires or an account is cancelled. Changing a policy does not delete history by itself.",
			SkipConfigured:  dataRetentionSkipFinancial,
		},
		{
			Category:        model.DataRetentionCategorySupportTickets,
			Title:           "Support tickets and images",
			AutoCleanable:   true,
			AllowedActions:  []string{model.DataRetentionActionDelete},
			DefaultBehavior: "no_automatic_deletion",
			Notes:           "When Root sets a period, later runs may delete only already closed or resolved tickets whose close or resolve time is older than that period, honoring a 90-day safety floor. Disk images are removed first; a file-delete failure leaves that ticket's records in place. Open, in-progress, and waiting tickets are not touched. Unconfigured tickets are not auto-deleted.",
			MinDays:         90,
		},
		{
			Category:        model.DataRetentionCategoryNotificationHistory,
			Title:           "Notification and announcement history",
			AutoCleanable:   false,
			AllowedActions:  []string{model.DataRetentionActionExclude},
			DefaultBehavior: "permanent_retention",
			Notes:           "In-site notification history is permanently retained against automatic cleanup. Only the user may manually delete their own deletable messages. Root-configured retention tasks must not delete it.",
		},
		{
			Category:        model.DataRetentionCategoryOperationAudit,
			Title:           "Operation audit logs",
			AutoCleanable:   true,
			AllowedActions:  []string{model.DataRetentionActionDelete},
			DefaultBehavior: "no_automatic_deletion",
			Notes:           "Unconfigured audit facts are not auto-deleted. When Root sets a period, later runs may delete operation and access-token audit rows older than that period. A config change never deletes history by itself.",
		},
		{
			Category:        model.DataRetentionCategoryTaskArtifacts,
			Title:           "Task inputs, outputs, and generated file references",
			AutoCleanable:   true,
			AllowedActions:  []string{model.DataRetentionActionDelete},
			DefaultBehavior: "no_automatic_deletion",
			Notes:           "Listed for Root configuration. Task artifacts follow official NewAPI and are kept by default. Automatic cleanup never deletes them after a configured period expires or an account is cancelled. Changing a policy does not delete history by itself.",
			SkipConfigured:  dataRetentionSkipArtifacts,
		},
	}
}

func dataRetentionSpec(category string) (dataRetentionCategorySpec, bool) {
	for _, spec := range dataRetentionCatalog() {
		if spec.Category == category {
			return spec, true
		}
	}
	return dataRetentionCategorySpec{}, false
}

// ListDataRetentionSettings returns every A.17.2 category and the current Root config.
func ListDataRetentionSettings() (gin.H, error) {
	policies, err := model.GetAdtyDataRetentionPolicies()
	if err != nil {
		return nil, err
	}
	views := make([]DataRetentionCategoryView, 0, len(dataRetentionCatalog()))
	for _, spec := range dataRetentionCatalog() {
		view := DataRetentionCategoryView{
			Category:        spec.Category,
			Title:           spec.Title,
			AutoCleanable:   spec.AutoCleanable,
			AllowedActions:  append([]string(nil), spec.AllowedActions...),
			DefaultBehavior: spec.DefaultBehavior,
			Notes:           spec.Notes,
		}
		if spec.MinDays > 0 {
			days := spec.MinDays
			view.MinRetentionDays = &days
		}
		if !spec.AutoCleanable {
			view.Action = model.DataRetentionActionExclude
		}
		if policy, ok := policies[spec.Category]; ok && policy.RetentionDays != nil && *policy.RetentionDays > 0 && spec.AutoCleanable {
			days := min(*policy.RetentionDays, maxDataRetentionDays)
			view.Configured = true
			view.RetentionDays = &days
			view.Action = policy.Action
		}
		views = append(views, view)
	}
	return gin.H{
		"categories":                      views,
		"login_security_env_default_days": common.UserSessionRevokedRetentionDays,
		"usage_log_min_retention_days":    int(LogCleanupRetention / (24 * time.Hour)),
	}, nil
}

type dataRetentionPolicyMutation struct {
	category string
	clear    bool
	days     int
	action   string
}

// SaveDataRetentionPolicies updates Root-configured periods. It does not start cleanup.
// Returns the prepared audit event ID (if persisted via AuditIntent) so callers can
// deduplicate generic middleware audit rows (R3-043).
func SaveDataRetentionPolicies(c *gin.Context, actorID int, actorName string, inputs []DataRetentionPolicyInput) (string, error) {
	if len(inputs) == 0 {
		return "", common.NewMessage("Invalid parameters")
	}
	if model.DB == nil {
		return "", errors.New("database unavailable")
	}
	seen := map[string]bool{}
	mutations := make([]dataRetentionPolicyMutation, 0, len(inputs))
	for _, input := range inputs {
		category := strings.TrimSpace(input.Category)
		if !model.IsDataRetentionCategory(category) || seen[category] {
			return "", common.NewMessage("Invalid parameters")
		}
		seen[category] = true
		spec, ok := dataRetentionSpec(category)
		if !ok {
			return "", common.NewMessage("Invalid parameters")
		}
		action := strings.TrimSpace(strings.ToLower(input.Action))
		if category == model.DataRetentionCategoryNotificationHistory || !spec.AutoCleanable {
			if input.Clear {
				continue
			}
			if input.RetentionDays != nil || (action != "" && action != model.DataRetentionActionExclude) {
				return "", common.NewMessage("Notification history cannot be given an automatic cleanup period")
			}
			continue
		}
		if input.Clear || (input.RetentionDays == nil && action == "") {
			mutations = append(mutations, dataRetentionPolicyMutation{
				category: category,
				clear:    true,
			})
			continue
		}
		if input.RetentionDays == nil || *input.RetentionDays < 1 || *input.RetentionDays > maxDataRetentionDays {
			return "", common.NewMessage("A retention period must be a positive number of days")
		}
		if action == "" {
			action = model.DataRetentionActionDelete
		}
		if !allowedDataRetentionAction(spec, action) {
			return "", common.NewMessage("Invalid parameters")
		}
		mutations = append(mutations, dataRetentionPolicyMutation{
			category: category,
			days:     *input.RetentionDays,
			action:   action,
		})
	}
	if len(mutations) == 0 {
		return "", nil
	}

	var auditEventID string
	var beforeSummary, afterSummary string
	now := common.GetTimestamp()
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		changes := make([]string, 0, len(mutations))
		beforeChanges := make([]string, 0, len(mutations))
		hasPolicyTable := tx.Migrator().HasTable(&model.AdtyDataRetentionPolicy{})
		for _, m := range mutations {
			var previous model.AdtyDataRetentionPolicy
			if hasPolicyTable {
				err := tx.Where("category = ?", m.category).Take(&previous).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
			prev := "unconfigured"
			if previous.RetentionDays != nil {
				prev = fmt.Sprintf("%s/%d", previous.Action, *previous.RetentionDays)
			}
			beforeChanges = append(beforeChanges, fmt.Sprintf("%s:%s", m.category, prev))
			if m.clear {
				if hasPolicyTable {
					if err := tx.Where("category = ?", m.category).Delete(&model.AdtyDataRetentionPolicy{}).Error; err != nil {
						return err
					}
				}
				changes = append(changes, fmt.Sprintf("%s:cleared", m.category))
				continue
			}
			days := m.days
			row := model.AdtyDataRetentionPolicy{
				Category:      m.category,
				RetentionDays: &days,
				Action:        m.action,
				UpdatedBy:     actorID,
				UpdatedAt:     now,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "category"}},
				DoUpdates: clause.AssignmentColumns([]string{"retention_days", "action", "updated_by", "updated_at"}),
			}).Create(&row).Error; err != nil {
				return err
			}
			changes = append(changes, fmt.Sprintf("%s:%s->%s/%d", m.category, prev, m.action, days))
		}
		siteID := 0
		beforeSummary = strings.Join(beforeChanges, "; ")
		afterSummary = strings.Join(changes, "; ")
		entry, err := model.PrepareOfficialAuditLog(c, model.OfficialAuditWrite{
			UserId: actorID, Username: actorName, ActorRole: common.RoleRootUser,
			SiteID: &siteID, ObjectType: "data_retention", ObjectID: "policy",
			Action: "data_retention.update", Category: model.AuditCategoryOperation,
			Content: "data retention policy updated: " + afterSummary,
			Before:  beforeSummary,
			After:   afterSummary,
			Result:  model.AuditResultSuccess, Success: true,
			Other: model.AuditOther{
				Op: &model.AuditOperation{
					Action: "data_retention.update",
					Params: model.AuditFields{"changes": afterSummary},
				},
			},
		})
		if err != nil {
			return err
		}
		if tx.Migrator().HasTable(&model.AuditIntent{}) {
			if err := model.AppendAuditIntentTx(tx, entry); err != nil {
				return err
			}
			auditEventID = entry.EventId
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if auditEventID != "" {
		deliveryCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = model.DeliverAuditIntent(deliveryCtx, auditEventID)
	} else {
		siteID := 0
		_ = model.WriteOfficialAuditLog(c, model.OfficialAuditWrite{
			UserId: actorID, Username: actorName, ActorRole: common.RoleRootUser,
			SiteID: &siteID, ObjectType: "data_retention", ObjectID: "policy",
			Action: "data_retention.update", Category: model.AuditCategoryOperation,
			Content: "data retention policy updated: " + afterSummary,
			Before:  beforeSummary,
			After:   afterSummary,
			Result:  model.AuditResultSuccess, Success: true,
			Other: model.AuditOther{
				Op: &model.AuditOperation{
					Action: "data_retention.update",
					Params: model.AuditFields{"changes": afterSummary},
				},
			},
		})
	}
	return auditEventID, nil
}

func allowedDataRetentionAction(spec dataRetentionCategorySpec, action string) bool {
	for _, allowed := range spec.AllowedActions {
		if allowed == action {
			return true
		}
	}
	return false
}

// ConfiguredDataRetentionDays returns the Root-configured period when one is set.
func ConfiguredDataRetentionDays(category string) (int, bool) {
	row, ok, err := model.GetAdtyDataRetentionPolicy(category)
	if err != nil || !ok || row.RetentionDays == nil || *row.RetentionDays <= 0 {
		return 0, false
	}
	if *row.RetentionDays > maxDataRetentionDays {
		return maxDataRetentionDays, true
	}
	return *row.RetentionDays, true
}

func usageLogCleanupFloor(now time.Time) time.Duration {
	floor := LogCleanupRetention
	if days, ok := ConfiguredDataRetentionDays(model.DataRetentionCategoryAPIUsageLogs); ok {
		configured := time.Duration(min(days, maxDataRetentionDays)) * 24 * time.Hour
		if configured > floor {
			return configured
		}
	}
	return floor
}

func ListDataRetentionRuns(limit int) ([]DataRetentionRunView, error) {
	runs, items, err := model.ListAdtyDataRetentionRuns(limit)
	if err != nil {
		return nil, err
	}
	views := make([]DataRetentionRunView, 0, len(runs))
	for _, run := range runs {
		views = append(views, DataRetentionRunView{
			Id:           run.Id,
			StartedAt:    run.StartedAt,
			FinishedAt:   run.FinishedAt,
			Trigger:      run.Trigger,
			Scope:        run.Scope,
			SystemTaskID: run.SystemTaskID,
			Items:        items[run.Id],
		})
	}
	return views, nil
}

// RunDataRetentionCleanup applies configured category rules and always records
// notification-history exclusion. Changing a policy never calls this function.
func RunDataRetentionCleanup(ctx context.Context, trigger, systemTaskID string, now time.Time) (*model.AdtyDataRetentionRun, []model.AdtyDataRetentionRunItem, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	if trigger == "" {
		trigger = model.DataRetentionTriggerScheduled
	}
	started := now.Unix()
	policies, err := model.GetAdtyDataRetentionPolicies()
	if err != nil {
		return nil, nil, err
	}
	run := &model.AdtyDataRetentionRun{
		StartedAt:    started,
		FinishedAt:   started,
		Trigger:      trigger,
		Scope:        model.DataRetentionScopePlatform,
		SystemTaskID: systemTaskID,
	}
	if err := model.DB.Create(run).Error; err != nil {
		return nil, nil, err
	}
	items := make([]model.AdtyDataRetentionRunItem, 0, len(dataRetentionCatalog()))
	for _, spec := range dataRetentionCatalog() {
		item := applyDataRetentionCategory(ctx, spec, policies[spec.Category], now)
		item.RunId = run.Id
		if err := model.DB.Create(&item).Error; err != nil {
			return run, items, err
		}
		items = append(items, item)
	}
	finished := time.Now().Unix()
	if finished < started {
		finished = started
	}
	run.FinishedAt = finished
	if err := model.DB.Model(run).Update("finished_at", finished).Error; err != nil {
		return run, items, err
	}
	return run, items, nil
}

func applyDataRetentionCategory(ctx context.Context, spec dataRetentionCategorySpec, policy model.AdtyDataRetentionPolicy, now time.Time) model.AdtyDataRetentionRunItem {
	executedAt := time.Now().Unix()
	if executedAt < now.Unix() {
		executedAt = now.Unix()
	}
	item := model.AdtyDataRetentionRunItem{
		Category:   spec.Category,
		Scope:      model.DataRetentionScopePlatform,
		ExecutedAt: executedAt,
	}
	if spec.Category == model.DataRetentionCategoryNotificationHistory || !spec.AutoCleanable {
		item.Rule = "exclude; permanent retention"
		item.SkipReason = dataRetentionSkipNotification
		return item
	}
	configured := policy.RetentionDays != nil && *policy.RetentionDays > 0
	if !configured {
		item.Rule = "unconfigured"
		item.SkipReason = dataRetentionSkipUnconfigured
		return item
	}
	action := policy.Action
	if action == "" {
		action = model.DataRetentionActionDelete
	}
	days := *policy.RetentionDays
	item.Rule = fmt.Sprintf("%s after %d days", action, days)
	if spec.MinDays > 0 && days < spec.MinDays {
		item.Rule = fmt.Sprintf("%s after %d days (safety floor %d days)", action, days, spec.MinDays)
	}
	if spec.SkipConfigured != "" {
		item.SkipReason = spec.SkipConfigured
		return item
	}
	if err := ctx.Err(); err != nil {
		item.FailureCount = 1
		item.SkipReason = err.Error()
		return item
	}
	effectiveDays := effectiveRetentionDays(days, spec.MinDays)
	if spec.Category == model.DataRetentionCategoryUserAccountProfile {
		cleared, err := model.ClearCancelledAccountProfileLeftovers(ctx, retentionCutoffUnix(now, effectiveDays), dataRetentionCleanupBatchSize)
		item.DeidentifyCount = cleared
		if err != nil {
			item.FailureCount = 1
			item.SkipReason = err.Error()
		}
		return item
	}
	if action == model.DataRetentionActionDeidentify {
		item.SkipReason = dataRetentionSkipDeidentify
		return item
	}
	deleted, failed, err := executeDataRetentionDelete(ctx, spec.Category, effectiveDays, now)
	item.DeleteCount = deleted
	item.FailureCount = failed
	if err != nil {
		if item.FailureCount == 0 {
			item.FailureCount = 1
		}
		item.SkipReason = err.Error()
	}
	return item
}

func effectiveRetentionDays(days, minDays int) int {
	if minDays > 0 && days < minDays {
		days = minDays
	}
	if days > maxDataRetentionDays {
		return maxDataRetentionDays
	}
	return days
}

func retentionCutoffUnix(now time.Time, days int) int64 {
	if days <= 0 {
		return 0
	}
	if days > maxDataRetentionDays {
		days = maxDataRetentionDays
	}
	nowUnix := now.Unix()
	if int64(days) > math.MaxInt64/secondsPerDay {
		return math.MinInt64
	}
	delta := int64(days) * secondsPerDay
	if nowUnix < 0 && delta > math.MaxInt64+nowUnix {
		return math.MinInt64
	}
	return nowUnix - delta
}

func executeDataRetentionDelete(ctx context.Context, category string, days int, now time.Time) (deleted, failed int64, err error) {
	days = effectiveRetentionDays(days, 0)
	cutoff := retentionCutoffUnix(now, days)
	switch category {
	case model.DataRetentionCategoryAPIUsageLogs:
		count, err := drainRetentionBatches(ctx, func() (int64, error) {
			return model.DeleteOldLogBatch(ctx, cutoff, dataRetentionCleanupBatchSize)
		})
		if err != nil {
			return count, 1, err
		}
		quota, err := drainRetentionBatches(ctx, func() (int64, error) {
			return model.DeleteOldQuotaDataBatch(ctx, cutoff, dataRetentionCleanupBatchSize)
		})
		if err != nil {
			return count + quota, 1, err
		}
		return count + quota, 0, nil
	case model.DataRetentionCategoryLoginSecurity:
		logs, err := drainRetentionBatches(ctx, func() (int64, error) {
			return model.DeleteOldLogsOfTypesBatch(ctx, []int{model.LogTypeLogin}, cutoff, dataRetentionCleanupBatchSize)
		})
		if err != nil {
			return logs, 1, err
		}
		audits, err := drainRetentionBatches(ctx, func() (int64, error) {
			return model.DeleteOldAuditLogsByCategoriesBatch(ctx, []string{model.AuditCategoryLogin, model.AuditCategorySecurity}, cutoff, dataRetentionCleanupBatchSize)
		})
		if err != nil {
			return logs + audits, 1, err
		}
		if err := ctx.Err(); err != nil {
			return logs + audits, 1, err
		}
		sessions, err := model.DeleteInactiveUserSessionsForRetention(now.Unix(), days)
		if err != nil {
			return logs + audits + sessions, 1, err
		}
		if err := ctx.Err(); err != nil {
			return logs + audits + sessions, 1, err
		}
		tokens, err := model.DeleteExpiredUserAccessTokensForRetention(now.Unix(), days)
		if err != nil {
			return logs + audits + sessions + tokens, 1, err
		}
		return logs + audits + sessions + tokens, 0, nil
	case model.DataRetentionCategoryOperationAudit:
		count, err := drainRetentionBatches(ctx, func() (int64, error) {
			return model.DeleteOldAuditLogsByCategoriesBatch(ctx, []string{model.AuditCategoryOperation, model.AuditCategoryAccessToken}, cutoff, dataRetentionCleanupBatchSize)
		})
		if err != nil {
			return count, 1, err
		}
		return count, 0, nil
	case model.DataRetentionCategorySupportTickets:
		return deleteExpiredSupportTickets(ctx, cutoff, dataRetentionCleanupBatchSize)
	default:
		return 0, 0, nil
	}
}

func deleteExpiredSupportTickets(ctx context.Context, cutoff int64, limit int) (deleted, failed int64, err error) {
	if limit <= 0 {
		limit = dataRetentionCleanupBatchSize
	}
	if model.DB == nil {
		return 0, 1, fmt.Errorf("database unavailable")
	}
	// Process the durable file queue even if a different ticket prevents this
	// run from expiring more cases.
	defer func() {
		fileFailures, cleanupErr := cleanExpiredTicketImages(ctx, limit)
		failed += fileFailures
		if cleanupErr != nil {
			failed++
			err = errors.Join(err, cleanupErr)
		}
	}()
	var lastID int64
	for {
		if err := ctx.Err(); err != nil {
			return deleted, failed + 1, err
		}
		var ids []int64
		lookup := model.DB.WithContext(ctx).Raw(`
			SELECT t.id
			FROM adty_tickets t
			WHERE t.status IN (?, ?)
			  AND t.id > ?
			  AND COALESCE(
			    (SELECT MAX(e.created_at) FROM adty_ticket_events e
			      WHERE e.ticket_id = t.id AND e.action = ? AND e.detail IN (?, ?)),
			    t.updated_at,
			    t.created_at
			  ) < ?
			ORDER BY t.id
			LIMIT ?
		`, model.AdtyTicketResolved, model.AdtyTicketClosed, lastID, "status", model.AdtyTicketResolved, model.AdtyTicketClosed, cutoff, limit)
		if err := lookup.Scan(&ids).Error; err != nil {
			return deleted, failed + 1, err
		}
		if len(ids) == 0 {
			return deleted, failed, nil
		}
		for _, id := range ids {
			lastID = id
			removed, fileFailed, ferr := deleteOneExpiredSupportTicket(ctx, id, cutoff)
			if ferr != nil {
				return deleted, failed + 1, ferr
			}
			if removed {
				deleted++
			} else if fileFailed {
				failed++
			}
		}
		if len(ids) < limit {
			return deleted, failed, nil
		}
	}
}

func deleteOneExpiredSupportTicket(ctx context.Context, ticketID int64, cutoff int64) (deleted, fileFailed bool, err error) {
	txErr := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ticket model.AdtyTicket
		if err := model.AdtyLockForUpdate(tx).First(&ticket, ticketID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if ticket.Status != model.AdtyTicketResolved && ticket.Status != model.AdtyTicketClosed {
			return nil
		}
		var closedAt int64
		if err := tx.Raw(`
			SELECT COALESCE(
			  (SELECT MAX(e.created_at) FROM adty_ticket_events e
			    WHERE e.ticket_id = ? AND e.action = ? AND e.detail IN (?, ?)),
			  NULLIF(?, 0),
			  ?
			)
		`, ticket.Id, "status", model.AdtyTicketResolved, model.AdtyTicketClosed, ticket.UpdatedAt, ticket.CreatedAt).Scan(&closedAt).Error; err != nil {
			return err
		}
		if closedAt >= cutoff {
			return nil
		}
		// Keep each storage name as a durable deletion job. Files remain intact
		// until the transaction removing the ticket and its evidence commits.
		if err := tx.Model(&model.AdtyTicketImage{}).Where("ticket_id=?", ticketID).UpdateColumn("delete_pending", true).Error; err != nil {
			return err
		}
		// The translation index also stores the full reply. Remove its copies
		// in the same transaction as the source message.
		translations := tx.Model(&model.AdtyTicketMessage{}).Select("'ticket-reply:' || CAST(id AS TEXT)").Where("ticket_id = ?", ticketID)
		if err := tx.Where("object_key IN (?)", translations).Delete(&model.AdtyTranslationJob{}).Error; err != nil {
			return err
		}
		if err := tx.Where("object_key IN (?)", translations).Delete(&model.AdtyTranslationHead{}).Error; err != nil {
			return err
		}
		var snapshotTeamID int64
		if ticket.TeamShared {
			snapshotTeamID = ticket.TeamId
		}
		if err := tx.Model(&model.AdtyTicketNotice{}).Where("ticket_id = ?", ticketID).Updates(map[string]any{
			"expired_at": time.Now().Unix(), "snapshot_team_id": snapshotTeamID, "site_id": ticket.HandlingSite(),
			"purpose": gorm.Expr("CASE WHEN COALESCE(purpose,'') <> '' THEN purpose WHEN user_id=? THEN ? ELSE ? END", ticket.UserId, model.AdtyTelegramPurposeUser, model.AdtyTelegramPurposeAdmin),
			"title":   "", "content": "", "telegram_pending": false,
		}).Error; err != nil {
			return err
		}
		if err := tx.Where("ticket_id = ?", ticketID).Delete(&model.AdtyTicketMessage{}).Error; err != nil {
			return err
		}
		if err := tx.Where("ticket_id = ?", ticketID).Delete(&model.AdtyTicketEvent{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", ticketID).Delete(&model.AdtyTicket{}).Error; err != nil {
			return err
		}
		deleted = true
		return nil
	})
	if txErr != nil {
		return false, false, txErr
	}
	return deleted, false, nil
}

var adtyPendingTicketAttachmentCleanupOnce sync.Once

// CleanupPendingAdtyTicketAttachments 独立清理 delete_pending = true 的孤儿工单附件，
// 即使工单保留天数清理未开启也可运行。返回本次成功清理数量与失败数量。
func CleanupPendingAdtyTicketAttachments(ctx context.Context, limit int) (int64, int64, error) {
	if model.DB == nil || !model.DB.Migrator().HasTable(&model.AdtyTicketImage{}) {
		return 0, 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = 200
	}
	var after, cleaned, failed int64
	for {
		if err := ctx.Err(); err != nil {
			return cleaned, failed, err
		}
		var images []model.AdtyTicketImage
		db := model.DB.WithContext(ctx)
		if err := db.Where("COALESCE(delete_pending, false) = true AND id > ?", after).Order("id").Limit(limit).Find(&images).Error; err != nil {
			return cleaned, failed, err
		}
		for _, image := range images {
			after = image.Id
			path, err := adtyTicketImageStoragePath(image)
			if err == nil && path != "" {
				err = os.Remove(path)
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				failed++
				common.SysError(common.LogText("cleanup pending ticket attachment %d failed: %s", image.Id, err.Error()))
				continue
			}
			if err := db.Where("id = ? AND COALESCE(delete_pending, false) = true", image.Id).Delete(&model.AdtyTicketImage{}).Error; err != nil {
				return cleaned, failed, err
			}
			cleaned++
		}
		if len(images) < limit {
			if cleaned > 0 || failed > 0 {
				common.SysLog(common.LogText("cleaned pending ticket attachments: cleaned=%d failed=%d", cleaned, failed))
			}
			return cleaned, failed, nil
		}
	}
}

// StartAdtyPendingTicketAttachmentCleanupTask starts an independent periodic
// cleanup loop for delete_pending ticket attachments regardless of whether
// ticket retention cleanup is configured.
func StartAdtyPendingTicketAttachmentCleanupTask() {
	adtyPendingTicketAttachmentCleanupOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			for {
				if _, _, err := CleanupPendingAdtyTicketAttachments(context.Background(), 200); err != nil {
					common.SysError(common.LogText("pending ticket attachment cleanup: %s", err.Error()))
				}
				<-ticker.C
			}
		}()
	})
}

// A crash after unlink is harmless: an absent file also completes the job.
func cleanExpiredTicketImages(ctx context.Context, limit int) (int64, error) {
	_, failed, err := CleanupPendingAdtyTicketAttachments(ctx, limit)
	return failed, err
}

func drainRetentionBatches(ctx context.Context, fn func() (int64, error)) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := fn()
		if err != nil {
			return total, err
		}
		total += n
		if n == 0 {
			return total, nil
		}
	}
}

type dataRetentionHandler struct{}

func (dataRetentionHandler) Type() string { return model.SystemTaskTypeDataRetention }

func (dataRetentionHandler) Enabled() bool { return true }

func (dataRetentionHandler) Interval() time.Duration { return dataRetentionSchedulerInterval }

func (dataRetentionHandler) NewPayload() any {
	return DataRetentionPayload{Trigger: model.DataRetentionTriggerScheduled}
}

func (dataRetentionHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	payload := DataRetentionPayload{Trigger: model.DataRetentionTriggerScheduled}
	_ = task.DecodePayload(&payload)
	if payload.Trigger == "" {
		payload.Trigger = model.DataRetentionTriggerScheduled
	}
	run, _, err := RunDataRetentionCleanup(ctx, payload.Trigger, task.TaskID, time.Now())
	if err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	result := DataRetentionResult{}
	if run != nil {
		result.RunID = run.Id
	}
	if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, result, ""); err != nil {
		logSystemTaskLockError(ctx, task, err)
	}
}

func init() {
	RegisterSystemTaskHandler(dataRetentionHandler{})
}
