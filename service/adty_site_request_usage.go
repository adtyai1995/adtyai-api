package service

import (
	"sort"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type AdtySiteUsageTotals struct {
	Quota    int64 `json:"quota"`
	Requests int64 `json:"requests"`
}

func (t *AdtySiteUsageTotals) add(quota int64) {
	t.Quota += quota
	t.Requests++
}

type AdtySiteUsageRow struct {
	Key string `json:"key"`
	AdtySiteUsageTotals
	USD decimal.Decimal `json:"usd"`
}

type AdtySiteUsageBreakdown struct {
	ByModel     []AdtySiteUsageRow  `json:"by_model"`
	ByGroup     []AdtySiteUsageRow  `json:"by_group"`
	ByPayer     []AdtySiteUsageRow  `json:"by_payer"`
	Unverified  AdtySiteUsageTotals `json:"unverified"`
	UnknownSite AdtySiteUsageTotals `json:"unknown_site"`
}

type usageBuckets map[string]AdtySiteUsageTotals

func (b usageBuckets) add(key string, quota int64) {
	item := b[key]
	item.add(quota)
	b[key] = item
}
func (b usageBuckets) rows() []AdtySiteUsageRow {
	rows := make([]AdtySiteUsageRow, 0, len(b))
	for key, t := range b {
		rows = append(rows, AdtySiteUsageRow{Key: key, AdtySiteUsageTotals: t, USD: decimal.NewFromInt(t.Quota).Div(common.QuotaPerUnitDecimal()).Round(2)})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Quota == rows[j].Quota {
			return rows[i].Key < rows[j].Key
		}
		return rows[i].Quota > rows[j].Quota
	})
	return rows[:min(len(rows), 100)]
}

const usageBillingContextKey = "adty_usage_billing_operation"

// Keep a copy with the log so archived requests remain attributable even if
// their primary-database operation has since been pruned.
type requestUsageOwner struct {
	SiteID      *int   `json:"site_id"`
	PayerKind   string `json:"payer_kind"`
	PayerTeamID int64  `json:"payer_team_id"`
}

func stampRequestUsageOwner(other *model.LogOther, op *model.BillingOperation) {
	if op == nil || op.UserId <= 0 || op.SiteId < 0 {
		return
	}
	owner := requestUsageOwner{SiteID: &op.SiteId, PayerKind: op.PayerKind, PayerTeamID: op.PayerTeamId}
	if owner.payer() != "" {
		other.SetAdmin("request_usage_owner", owner)
	}
}

type usageLogEvidence struct {
	Owner        *requestUsageOwner `json:"request_usage_owner"`
	SiteID       *int               `json:"site_id"`
	OperationKey string             `json:"billing_operation_key"`
}

type usageBillingEvidence struct {
	OperationKey    string
	SiteId          *int
	UserId, TokenId int
	PayerKind       string
	PayerTeamId     int64
}

func (op requestUsageOwner) payer() string {
	switch op.PayerKind {
	case model.BillingPayerTeam:
		if op.PayerTeamID > 0 {
			return op.PayerKind
		}
	case model.BillingPayerPersonal:
		if op.PayerTeamID == 0 {
			return op.PayerKind
		}
	case model.BillingPayerOwner:
		if op.PayerTeamID == 0 && op.SiteID != nil && *op.SiteID > 0 {
			return op.PayerKind
		}
	}
	return ""
}

// Logs and billing operations can be on separate PostgreSQL connections.
// Resolve the stored operation key (or the original request ID) in bounded
// batches. Neither today's user site nor today's token binding is evidence.
func GetAdtySiteUsageBreakdown(siteID int, from, to string) (*AdtySiteUsageBreakdown, error) {
	loc, _, err := loadAdtyBusinessLocation()
	if err != nil {
		return nil, err
	}
	from, to, err = adtyStatsRange(from, to, loc)
	if err != nil {
		return nil, err
	}
	start, end, err := adtyUsagePeriod(siteID, from, to, loc)
	if err != nil {
		return nil, err
	}
	byModel, byGroup, byPayer := usageBuckets{}, usageBuckets{}, usageBuckets{}
	result := new(AdtySiteUsageBreakdown)
	selectCols := "id, created_at, request_id, user_id, token_id, model_name, " + model.AdtyGroupColumn() + ", quota, other"
	processBatch := func(batch []model.Log) error {
		if len(batch) == 0 {
			return nil
		}
		evidence := make([]usageLogEvidence, len(batch))
		keys := make([]string, 0, len(batch))
		for i, l := range batch {
			var data struct {
				Admin usageLogEvidence `json:"admin_info"`
			}
			if common.Unmarshal([]byte(l.Other), &data) == nil {
				evidence[i] = data.Admin
			}
			if owner := evidence[i].Owner; owner != nil && owner.SiteID != nil && owner.payer() != "" && l.UserId > 0 {
				continue
			}
			if evidence[i].OperationKey == "" && l.RequestId != "" {
				evidence[i].OperationKey = "request:" + l.RequestId
			}
			if key := evidence[i].OperationKey; key != "" {
				keys = append(keys, key)
			}
		}
		ops := map[string]usageBillingEvidence{}
		if len(keys) > 0 {
			var matches []usageBillingEvidence
			if err := model.DB.Model(&model.BillingOperation{}).Select("operation_key,site_id,user_id,token_id,payer_kind,payer_team_id").Where("operation_key IN ?", keys).Scan(&matches).Error; err != nil {
				return err
			}
			for _, op := range matches {
				ops[op.OperationKey] = op
			}
		}
		for i, l := range batch {
			site := evidence[i].SiteID
			payer := ""
			if owner := evidence[i].Owner; owner != nil && owner.SiteID != nil && owner.payer() != "" && l.UserId > 0 {
				site, payer = owner.SiteID, owner.payer()
			} else if op, ok := ops[evidence[i].OperationKey]; ok && op.UserId == l.UserId && l.UserId > 0 && op.TokenId == l.TokenId {
				if site != nil && op.SiteId != nil && *site != *op.SiteId {
					site = nil
				} else {
					if op.SiteId != nil {
						site = op.SiteId
					}
					payer = (requestUsageOwner{SiteID: site, PayerKind: op.PayerKind, PayerTeamID: op.PayerTeamId}).payer()
				}
			}
			quota := int64(l.Quota)
			switch {
			case site == nil || *site < 0:
				result.UnknownSite.add(quota)
			case *site != siteID:
				continue
			case payer == "":
				result.Unverified.add(quota)
			default:
				byModel.add(l.ModelName, quota)
				byGroup.add(l.Group, quota)
				byPayer.add(payer, quota)
			}
		}
		return nil
	}
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		const batchSize = 500
		cursorCreatedAt := start
		for cursorCreatedAt < end {
			var batch []model.Log
			if err = model.LOG_DB.Model(&model.Log{}).
				Select(selectCols).
				Where("type = ? AND created_at >= ? AND created_at < ?", model.LogTypeConsume, cursorCreatedAt, end).
				Order("created_at ASC, request_id ASC").
				Limit(batchSize).
				Find(&batch).Error; err != nil {
				return nil, err
			}
			if len(batch) == 0 {
				break
			}
			if len(batch) < batchSize {
				if err = processBatch(batch); err != nil {
					return nil, err
				}
				break
			}
			firstSec := batch[0].CreatedAt
			lastSec := batch[len(batch)-1].CreatedAt
			if lastSec > firstSec {
				cut := len(batch)
				for cut > 0 && batch[cut-1].CreatedAt == lastSec {
					cut--
				}
				if err = processBatch(batch[:cut]); err != nil {
					return nil, err
				}
				cursorCreatedAt = lastSec
				continue
			}
			// All batchSize rows share the exact same created_at second: drain
			// that single second deterministically so duplicate (created_at, request_id)
			// rows are neither skipped nor double-counted.
			for offset := 0; ; offset += batchSize {
				var secBatch []model.Log
				if err = model.LOG_DB.Model(&model.Log{}).
					Select(selectCols).
					Where("type = ? AND created_at = ?", model.LogTypeConsume, firstSec).
					Order("request_id ASC, user_id ASC, token_id ASC, model_name ASC, quota ASC, other ASC").
					Offset(offset).
					Limit(batchSize).
					Find(&secBatch).Error; err != nil {
					return nil, err
				}
				if len(secBatch) == 0 {
					break
				}
				if err = processBatch(secBatch); err != nil {
					return nil, err
				}
				if len(secBatch) < batchSize {
					break
				}
			}
			cursorCreatedAt = firstSec + 1
		}
	} else {
		var logs []model.Log
		err = model.LOG_DB.Model(&model.Log{}).
			Select(selectCols).
			Where("type = ? AND created_at >= ? AND created_at < ?", model.LogTypeConsume, start, end).
			FindInBatches(&logs, 500, func(_ *gorm.DB, _ int) error {
				return processBatch(logs)
			}).Error
	}
	if err != nil {
		return nil, err
	}
	result.ByModel, result.ByGroup, result.ByPayer = byModel.rows(), byGroup.rows(), byPayer.rows()
	return result, nil
}

// Closed business days retain their original half-open settlement boundaries.
func adtyUsagePeriod(siteID int, from, to string, loc *time.Location) (int64, int64, error) {
	start, end := adtyDayUnix(from, false, loc), adtyDayUnix(to, true, loc)+1
	previous := time.Unix(start, 0).In(loc).AddDate(0, 0, -1).Format(time.DateOnly)
	next := time.Unix(end, 0).In(loc).Format(time.DateOnly)
	var sheets []model.AdtySiteSettlementSheet
	if err := model.DB.Select("business_day,day_start,day_end").Where("site_id = ? AND business_day IN ? AND day_end > day_start", siteID, []string{previous, from, to, next}).Find(&sheets).Error; err != nil {
		return 0, 0, err
	}
	windows := map[string]model.AdtySiteSettlementSheet{}
	for _, sheet := range sheets {
		windows[sheet.BusinessDay] = sheet
	}
	if w, ok := windows[from]; ok {
		start = w.DayStart
	} else if w, ok := windows[previous]; ok {
		start = w.DayEnd
	}
	if w, ok := windows[to]; ok {
		end = w.DayEnd
	} else if w, ok := windows[next]; ok {
		end = w.DayStart
	}
	return start, end, nil
}
