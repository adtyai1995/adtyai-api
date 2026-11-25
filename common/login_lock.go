package common

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

// 口令失败计数：凡是「拿口令换凭据」的入口都必须共用这一份，否则那个入口就是
// 绕开失败锁定与限流的密码预言机（ISS-R4-0105）。
//
// 有这个计数之后，同一个用户名在所有入口上的失败次数合在一起算：登录、团队
// 注册链接这类匿名入口都一样。多副本共享靠 Redis；没开 Redis 时退回进程内存，
// 语义是「每个进程各自计数」。

// LoginLockLimit 是同一个用户名在 LoginLockWindow 内的最大失败次数。
const LoginLockLimit = 8

// LoginLockWindow 是失败计数的滑动窗口：每次失败都从当下重新计时。
const LoginLockWindow = 15 * time.Minute

// ErrLoginLockUnavailable 表示计数存不上（配置了 Redis 却连不上）。这种情况下
// 入口必须失败关闭，不能悄悄放行。
var ErrLoginLockUnavailable = errors.New("login lock store unavailable")

type loginFailure struct {
	count int
	until time.Time
}

const (
	loginFailureSweepThreshold = 10000
	loginFailureSweepInterval  = 30 * time.Second
	maxLoginFailureEntries     = 50000
)

var (
	loginFailuresMu        sync.Mutex
	loginFailures          = map[string]loginFailure{}
	loginFailuresLastSweep time.Time
)

// LoginLockUserKey returns the canonical per-account lockout identifier.
// Prefer LoginLockedUser / RecordLoginFailureUser / ClearLoginFailuresUser so
// user-supplied usernames can never collide with internal per-user lock keys.
func LoginLockUserKey(userID int) string {
	if userID <= 0 {
		return ""
	}
	return fmt.Sprintf("uid:%d", userID)
}

func loginLockNameKey(username string) string {
	norm := strings.ToLower(strings.TrimSpace(username))
	if norm == "" {
		return ""
	}
	return "name:" + norm
}

func loginLockUserKey(userID int) string {
	if userID <= 0 {
		return ""
	}
	return fmt.Sprintf("uid:%d", userID)
}

func loginLockRedisKey(scopedKey string) string {
	return "adty:login-fail:" + scopedKey
}

func loginLockUsesRedis() bool {
	return RedisEnabled && RDB != nil
}

func loginLockedScoped(key string) bool {
	if key == "" {
		return false
	}
	if RedisEnabled && RDB == nil {
		return true
	}
	if loginLockUsesRedis() {
		n, err := RDB.Get(context.Background(), loginLockRedisKey(key)).Int()
		if err == redis.Nil {
			return false
		}
		if err != nil {
			return true
		}
		return n >= LoginLockLimit
	}
	loginFailuresMu.Lock()
	defer loginFailuresMu.Unlock()
	entry, ok := loginFailures[key]
	if !ok || time.Now().After(entry.until) {
		delete(loginFailures, key)
		return false
	}
	return entry.count >= LoginLockLimit
}

func recordLoginFailureScoped(key string) error {
	if key == "" {
		return nil
	}
	if RedisEnabled && RDB == nil {
		return ErrLoginLockUnavailable
	}
	if loginLockUsesRedis() {
		ctx := context.Background()
		redisKey := loginLockRedisKey(key)
		if err := RDB.Incr(ctx, redisKey).Err(); err != nil {
			return err
		}
		if err := RDB.Expire(ctx, redisKey, LoginLockWindow).Err(); err != nil {
			return err
		}
		return nil
	}
	loginFailuresMu.Lock()
	defer loginFailuresMu.Unlock()
	now := time.Now()
	entry, exists := loginFailures[key]
	if exists && now.After(entry.until) {
		entry = loginFailure{}
		exists = false
	}
	if !exists && len(loginFailures) >= maxLoginFailureEntries {
		loginFailuresLastSweep = now
		for name, stored := range loginFailures {
			if now.After(stored.until) {
				delete(loginFailures, name)
			}
		}
		if len(loginFailures) >= maxLoginFailureEntries {
			// Batch-evict unlocked entries only (preferring single-failure noise
			// first) so random-username floods never evict active lockouts or
			// force O(N) scans on every request.
			targetSize := maxLoginFailureEntries - maxLoginFailureEntries/10
			if targetSize < 0 {
				targetSize = 0
			}
			for name, stored := range loginFailures {
				if len(loginFailures) <= targetSize {
					break
				}
				if stored.count == 1 && name != key {
					delete(loginFailures, name)
				}
			}
			if len(loginFailures) >= maxLoginFailureEntries {
				for name, stored := range loginFailures {
					if len(loginFailures) <= targetSize {
						break
					}
					if stored.count < LoginLockLimit && name != key {
						delete(loginFailures, name)
					}
				}
			}
		}
		// If locked entries alone saturate capacity, fail closed rather than
		// dropping an active lockout or growing memory unboundedly.
		if len(loginFailures) >= maxLoginFailureEntries {
			return ErrLoginLockUnavailable
		}
	}
	entry.count++
	entry.until = now.Add(LoginLockWindow)
	loginFailures[key] = entry
	if len(loginFailures) > loginFailureSweepThreshold && now.Sub(loginFailuresLastSweep) >= loginFailureSweepInterval {
		loginFailuresLastSweep = now
		for name, stored := range loginFailures {
			if now.After(stored.until) {
				delete(loginFailures, name)
			}
		}
	}
	return nil
}

func clearLoginFailuresScoped(key string) {
	if key == "" {
		return
	}
	if loginLockUsesRedis() {
		_ = RDB.Del(context.Background(), loginLockRedisKey(key)).Err()
	}
	loginFailuresMu.Lock()
	delete(loginFailures, key)
	loginFailuresMu.Unlock()
}

// LoginLocked reports whether this user-supplied username/email has used up its password attempts.
// 配了 Redis 却连不上时失败关闭：宁可拦下入口，也不能让猜口令无限次。
func LoginLocked(username string) bool {
	return loginLockedScoped(loginLockNameKey(username))
}

// LoginLockedUser reports whether this account ID has used up its password attempts.
func LoginLockedUser(userID int) bool {
	return loginLockedScoped(loginLockUserKey(userID))
}

// RecordLoginFailure 记一次口令猜错（按用户输入的登录标识隔离命名空间）。
func RecordLoginFailure(username string) error {
	return recordLoginFailureScoped(loginLockNameKey(username))
}

// RecordLoginFailureUser 记一次口令猜错（按内部账号 ID 隔离命名空间）。
func RecordLoginFailureUser(userID int) error {
	return recordLoginFailureScoped(loginLockUserKey(userID))
}

// ClearLoginFailures 在一个口令验证成功之后清掉这个用户名的失败计数。
func ClearLoginFailures(username string) {
	clearLoginFailuresScoped(loginLockNameKey(username))
}

// ClearLoginFailuresUser 在一个口令验证成功之后清掉该账号 ID 的失败计数。
func ClearLoginFailuresUser(userID int) {
	clearLoginFailuresScoped(loginLockUserKey(userID))
}

// ResetLoginFailuresForTest 清空进程内的失败计数（测试用，等价于换一个进程）。
func ResetLoginFailuresForTest() {
	loginFailuresMu.Lock()
	defer loginFailuresMu.Unlock()
	loginFailures = map[string]loginFailure{}
	loginFailuresLastSweep = time.Time{}
}
