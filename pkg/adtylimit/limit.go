// Package adtylimit 按用户限制每分钟请求数（RPM）、每分钟 token 数（TPM）和同时进行的请求数（任务 7.4）。
// 开了 Redis 用 Redis（多实例共享），没开用进程内存（只适合单实例）。
//
// TPM 是事后累计：token 数在请求结束写消费日志时才加上，同一分钟里并发进来的大请求拦不住，
// 只能挡住之后的请求。
package adtylimit

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Limits 是一个用户的限额，0 表示不限。
type Limits struct {
	RPM         int
	TPM         int
	Concurrency int
}

// Store 是计数的存放处。
type Store interface {
	// IncrRPM 给这一分钟的请求数加一，返回加之后的值。
	IncrRPM(userID int, minute int64) (int64, error)
	// TPM 返回这一分钟已经累计的 token 数。
	TPM(userID int, minute int64) (int64, error)
	// AddTPM 给这一分钟加上 token 数。
	AddTPM(userID int, minute int64, tokens int64) error
	// Acquire 在没超过上限时登记一个进行中的请求（ttl 后自动过期），返回是否登记成功。
	Acquire(userID int, member string, limit int, now time.Time, ttl time.Duration) (bool, error)
	// Release 去掉一个进行中的请求。
	Release(userID int, member string) error
}

// leaseRenewer 由支持对仍在运行的活跃请求续租的 Store 实现。
type leaseRenewer interface {
	Renew(userID int, member string, now time.Time, ttl time.Duration) error
}

// Reason 是拒绝的原因。
type Reason string

const (
	ReasonNone        Reason = ""
	ReasonRPM         Reason = "rpm"
	ReasonTPM         Reason = "tpm"
	ReasonConcurrency Reason = "concurrency"
)

// ConcurrencyTTL 是进行中请求的单次故障回收租期：请求没走到释放（进程崩溃）时，到期自动清掉；
// 仍在运行中的长流式/Realtime 请求会在活跃期间持续续租，直到 release 被调用。
const ConcurrencyTTL = 15 * time.Minute

// ConcurrencyRenewInterval 是后台对活跃请求自动续租的周期。
const ConcurrencyRenewInterval = ConcurrencyTTL / 3

type activeLeaseKey struct {
	store  Store
	userID int
}

type activeLease struct {
	id        uint64
	member    string
	startedAt time.Time
	stop      chan struct{}
}

var (
	activeLeasesMu  sync.Mutex
	activeLeaseSeq  atomic.Uint64
	activeLeasesMap = map[activeLeaseKey]map[uint64]*activeLease{}
)

func renewActiveUserLeases(store Store, userID int, now time.Time) error {
	renewer, ok := store.(leaseRenewer)
	if !ok {
		return nil
	}
	activeLeasesMu.Lock()
	userLeases := activeLeasesMap[activeLeaseKey{store: store, userID: userID}]
	members := make([]string, 0, len(userLeases))
	for _, lease := range userLeases {
		if !now.Before(lease.startedAt) {
			members = append(members, lease.member)
		}
	}
	activeLeasesMu.Unlock()
	for _, member := range members {
		if err := renewer.Renew(userID, member, now, ConcurrencyTTL); err != nil {
			return err
		}
	}
	return nil
}

func trackActiveLease(store Store, userID int, member string, now time.Time) func() {
	id := activeLeaseSeq.Add(1)
	key := activeLeaseKey{store: store, userID: userID}
	lease := &activeLease{
		id:        id,
		member:    member,
		startedAt: now,
		stop:      make(chan struct{}),
	}
	activeLeasesMu.Lock()
	userLeases := activeLeasesMap[key]
	if userLeases == nil {
		userLeases = map[uint64]*activeLease{}
		activeLeasesMap[key] = userLeases
	}
	userLeases[id] = lease
	activeLeasesMu.Unlock()

	if renewer, ok := store.(leaseRenewer); ok {
		go func(base time.Time) {
			ticker := time.NewTicker(ConcurrencyRenewInterval)
			defer ticker.Stop()
			ticks := time.Duration(0)
			for {
				select {
				case <-lease.stop:
					return
				case wallNow := <-ticker.C:
					ticks += ConcurrencyRenewInterval
					renewNow := base.Add(ticks)
					if wallNow.After(renewNow) {
						renewNow = wallNow
					}
					_ = renewer.Renew(userID, member, renewNow, ConcurrencyTTL)
				}
			}
		}(now)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			close(lease.stop)
			shouldRelease := true
			activeLeasesMu.Lock()
			if current := activeLeasesMap[key]; current != nil {
				delete(current, id)
				for _, other := range current {
					if other.member == member {
						shouldRelease = false
						break
					}
				}
				if len(current) == 0 {
					delete(activeLeasesMap, key)
				}
			}
			activeLeasesMu.Unlock()
			if shouldRelease {
				_ = store.Release(userID, member)
			}
		})
	}
}

// MinuteOf 是分钟编号，RPM 和 TPM 都按它分桶。
func MinuteOf(now time.Time) int64 { return now.Unix() / 60 }

// RetryAfter 是到下一分钟还剩的秒数，至少 1 秒。
func RetryAfter(now time.Time) int64 { return max(60-now.Unix()%60, 1) }

// Redis 的键（内存实现直接按分钟和用户分桶）。
func rpmKey(userID int, minute int64) string {
	return "ADTY:RPM:" + strconv.Itoa(userID) + ":" + strconv.FormatInt(minute, 10)
}

func tpmKey(userID int, minute int64) string {
	return "ADTY:TPM:" + strconv.Itoa(userID) + ":" + strconv.FormatInt(minute, 10)
}

func concurrencyKey(userID int) string { return "ADTY:CONC:" + strconv.Itoa(userID) }

// Check 按顺序检查 TPM、RPM、并发；通过时如果登记了并发，release 非空，请求结束时调用。
func Check(store Store, userID int, limits Limits, member string, now time.Time) (release func(), reason Reason, err error) {
	minute := MinuteOf(now)
	if limits.TPM > 0 {
		used, err := store.TPM(userID, minute)
		if err != nil {
			return nil, ReasonNone, err
		}
		if used >= int64(limits.TPM) {
			return nil, ReasonTPM, nil
		}
	}
	if limits.RPM > 0 {
		count, err := store.IncrRPM(userID, minute)
		if err != nil {
			return nil, ReasonNone, err
		}
		if count > int64(limits.RPM) {
			return nil, ReasonRPM, nil
		}
	}
	if limits.Concurrency <= 0 {
		return nil, ReasonNone, nil
	}
	ok, err := store.Acquire(userID, member, limits.Concurrency, now, ConcurrencyTTL)
	if err != nil {
		return nil, ReasonNone, err
	}
	if !ok {
		return nil, ReasonConcurrency, nil
	}
	return trackActiveLease(store, userID, member, now), ReasonNone, nil
}
