package service

import (
	"context"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

type adtyMaintenanceHook struct {
	name string
	fn   func(now int64)
}

var (
	adtyMaintenanceMu    sync.RWMutex
	adtyMaintenanceHooks []adtyMaintenanceHook
	adtyMaintenanceOnce  sync.Once
	// AdtyExpiredHook 在过期任务把订单改成 expired 之后调用（P5 按通道计连续未付）。
	AdtyExpiredHook func(expired []model.ExpiredAdtyTopUp)
)

// RegisterAdtyMaintenanceHook 登记每轮维护都要跑的钩子（P5 的补查、连续未付、探活）。
func RegisterAdtyMaintenanceHook(name string, fn func(now int64)) {
	adtyMaintenanceMu.Lock()
	defer adtyMaintenanceMu.Unlock()
	for i, hook := range adtyMaintenanceHooks {
		if hook.name == name {
			adtyMaintenanceHooks[i].fn = fn
			return
		}
	}
	adtyMaintenanceHooks = append(adtyMaintenanceHooks, adtyMaintenanceHook{name: name, fn: fn})
}

// RunAdtyTopUpMaintenanceOnce 跑一轮：先跑补查与恢复钩子确认远端支付状态，再对确认未付且过缓冲期的 pending 订单执行过期释放（ISS-R1-0010）。
func RunAdtyTopUpMaintenanceOnce(now int64) {
	adtyMaintenanceMu.RLock()
	hooks := append([]adtyMaintenanceHook(nil), adtyMaintenanceHooks...)
	adtyMaintenanceMu.RUnlock()
	for _, hook := range hooks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.SysError(common.LogText("adty maintenance hook %s panic: %v", hook.name, r))
				}
			}()
			hook.fn(now)
		}()
	}
	if err := ReleaseAdtySiteInviteCommissions(time.Unix(now, 0)); err != nil {
		common.SysError(common.LogText("release site invite commissions: %s", err.Error()))
	}
	if _, err := ResumePendingAdtyGiftFulfillments(0); err != nil {
		common.SysError(common.LogText("resume main-site pending gift fulfillments: %s", err.Error()))
	}
	if model.DB != nil && model.DB.Migrator().HasTable(&model.AdtyGiftFulfillment{}) {
		var pendingGiftSites []int
		if err := model.DB.Model(&model.AdtyGiftFulfillment{}).Distinct("site_id").
			Where("site_id > 0 AND status = ?", model.AdtyGiftFulfillmentPending).
			Pluck("site_id", &pendingGiftSites).Error; err != nil {
			common.SysError(common.LogText("query pending subsite gift fulfillments: %s", err.Error()))
		} else {
			for _, siteID := range pendingGiftSites {
				if _, err := ResumePendingAdtyGiftFulfillments(siteID); err != nil {
					common.SysError(common.LogText("resume subsite pending gift fulfillments site=%d: %s", siteID, err.Error()))
				}
			}
		}
	}
	verifyExpiredOrdersUnpaidBeforeExpire(now)
	var cursorExpireAt, cursorID int64
	for page := 0; page < 50; page++ {
		expired, scanned, nextExpireAt, nextID, err := model.ExpireAdtyTopUpsAfter(now, cursorExpireAt, cursorID, 200)
		if len(expired) > 0 && AdtyExpiredHook != nil {
			AdtyExpiredHook(expired)
		}
		if err != nil {
			common.SysError(common.LogText("expire adty topups: %s", err.Error()))
			break
		}
		if scanned < 200 || (nextExpireAt == cursorExpireAt && nextID == cursorID) {
			break
		}
		cursorExpireAt, cursorID = nextExpireAt, nextID
	}
}

// verifyExpiredOrdersUnpaidBeforeExpire confirms via remote query outside row
// locks that expired gateway-backed orders have not paid before ExpireAdtyTopUps
// releases their gift guarantee reserves (ISS-R1-0010).
func verifyExpiredOrdersUnpaidBeforeExpire(now int64) {
	if model.DB == nil {
		return
	}
	cutoff := now - adtyExpireReleaseBufferSeconds
	var orders []model.AdtyTopupOrder
	err := model.DB.Table("adty_topup_orders AS a").Select("a.*").
		Joins("JOIN top_ups t ON t.trade_no = a.trade_no AND t.user_id = a.user_id AND t.payment_provider = a.provider").
		Where("t.status = ? AND a.expire_at > 0 AND a.expire_at <= ? AND (a.last_query_at < a.expire_at OR a.last_query_at <= ?)",
			common.TopUpStatusPending, cutoff, now-60).
		Order("a.last_query_at asc, a.expire_at asc, a.id asc").Limit(200).Scan(&orders).Error
	if err != nil {
		common.SysError(common.LogText("list adty pre-expire query orders: %s", err.Error()))
		return
	}
	for i := range orders {
		ext := &orders[i]
		requiresRemote, checkErr := adtyOrderRequiresRemoteUnpaidConfirm(model.DB, ext)
		if checkErr != nil || !requiresRemote {
			continue
		}
		var latest model.AdtyOrderPaymentEvent
		if qErr := model.DB.Where("trade_no = ? AND source = ?", ext.TradeNo, model.AdtyPaymentSourceQuery).
			Order("id desc").Limit(1).Find(&latest).Error; qErr != nil {
			continue
		}
		if latest.Id > 0 && latest.Outcome == "unpaid" && latest.CreatedAt >= ext.ExpireAt {
			continue
		}
		_ = model.UpdateAdtyTopupOrderFields(ext.TradeNo, map[string]any{"last_query_at": now})
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		_ = RecoverAdtyHostedPayment(ctx, ext)
		cancel()
	}
}

// StartAdtyTopUpMaintenance 只在主节点启动；间隔默认 60 秒（ADTY_TOPUP_MAINTENANCE_INTERVAL_SECONDS）。
func StartAdtyTopUpMaintenance() {
	if !common.IsMasterNode {
		return
	}
	adtyMaintenanceOnce.Do(func() {
		interval := common.GetEnvOrDefault("ADTY_TOPUP_MAINTENANCE_INTERVAL_SECONDS", 60)
		if interval < 5 {
			interval = 5
		}
		go func() {
			RunAdtyTopUpMaintenanceOnce(time.Now().Unix())
			ticker := time.NewTicker(time.Duration(interval) * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				RunAdtyTopUpMaintenanceOnce(time.Now().Unix())
			}
		}()
	})
}

func init() {
	RegisterAdtyMaintenanceHook("expire_root_gifts", expireAdtyRootGifts)
	adtyMasterTasks = append(adtyMasterTasks, StartAdtyTopUpMaintenance)
}

// Every pass catches up eligible calendar days and any released reservations.
// Running again within a day is safe because each cleared portion is journaled.
func expireAdtyRootGifts(now int64) {
	if model.DB == nil {
		return
	}
	loc, _, err := loadAdtyBusinessLocation()
	if err != nil {
		common.SysError(common.LogText("load gift expiry business timezone: %s", err.Error()))
		return
	}
	if _, err := model.ExpireAdtyGiftBatches(time.Unix(now, 0).In(loc)); err != nil {
		common.SysError(common.LogText("expire Root gift batches: %s", err.Error()))
	}
}
