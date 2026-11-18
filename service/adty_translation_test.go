package service

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ISS-R1-0144: Enabled=false 时动态翻译和 provider 出口都不得发起外部调用，也不得累加用量。
func TestAdtyTranslationDisabledSkipsProviderAndUsage(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")
	prepareReferralDialectDB(t, "sqlite")

	var dialed int32
	oldClient := adtyTranslationHTTPClient
	adtyTranslationHTTPClient = func(time.Duration) *http.Client {
		atomic.AddInt32(&dialed, 1)
		return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("should not dial when disabled")
		})}
	}
	t.Cleanup(func() { adtyTranslationHTTPClient = oldClient })

	_, err := SaveAdtyTranslationConfig(AdtyTranslationInput{
		Mode:      model.AdtyTranslationModeOwn,
		Protocol:  "openai",
		BaseURL:   "https://translator.example/v1",
		Token:     "secret-key",
		ModelName: "translator",
		Enabled:   false,
	})
	require.NoError(t, err)

	_, providerErr := adtyTranslateViaProvider("translator", "金额 12.50 USD")
	require.ErrorIs(t, providerErr, ErrAdtyTranslationDisabled)
	assert.Zero(t, atomic.LoadInt32(&dialed))

	res, err := TranslateAdtyDynamic("notice:disabled", "金额 12.50 USD，订单 A123456，不退款", 0)
	require.NoError(t, err)
	assert.Equal(t, model.AdtyTranslationFailed, res.Status)
	assert.Equal(t, AdtyTranslationNotReady, res.Notice)
	assert.Empty(t, res.English)
	assert.Zero(t, atomic.LoadInt32(&dialed))

	usage, err := AdtyTranslationUsageToday()
	require.NoError(t, err)
	assert.Zero(t, usage.Calls)
}

// ISS-R1-0145: 派发前预占版本，旧源文延迟完成不得覆盖后续失败或超长降级的新源文。
func TestAdtyTranslationReservesVersionAndRejectsStaleCompletion(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")
	db := prepareReferralDialectDB(t, "sqlite")

	_, err := SaveAdtyTranslationConfig(AdtyTranslationInput{
		Mode:      model.AdtyTranslationModeSet,
		ModelName: "translator",
		MaxChars:  30,
		Enabled:   true,
	})
	require.NoError(t, err)

	oldTranslate := AdtyTranslateFunc
	t.Cleanup(func() { AdtyTranslateFunc = oldTranslate })

	// v1 初始成功
	AdtyTranslateFunc = func(_, _ string) (string, error) {
		return "Initial 10 USD", nil
	}
	v1, err := TranslateAdtyDynamic("notice:race", "初始 10 USD", 0)
	require.NoError(t, err)
	require.Equal(t, 1, v1.Version)
	require.Equal(t, model.AdtyTranslationReady, v1.Status)

	inFlightEntered := make(chan struct{})
	releaseOldCall := make(chan struct{})
	AdtyTranslateFunc = func(_, text string) (string, error) {
		if text == "旧源文 12.50 USD" {
			close(inFlightEntered)
			<-releaseOldCall
			return "Old source 12.50 USD", nil
		}
		return "", errors.New("unexpected call")
	}

	var oldResult *AdtyTranslationResult
	var oldErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		oldResult, oldErr = TranslateAdtyDynamic("notice:race", "旧源文 12.50 USD", 0)
	}()

	<-inFlightEntered

	// 在旧调用阻塞期间，新源文因超长走 fallbackAdtyTranslation。
	longSourceB := "新源文超长降级 99.00 USD 并且长度超过三十个字符上限的中文公告内容"
	bResult, err := TranslateAdtyDynamic("notice:race", longSourceB, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, bResult.Version)
	assert.Equal(t, model.AdtyTranslationFailed, bResult.Status)

	// 放行旧源文 A，它的结果是旧版本 v2，必须被拒绝，不能覆盖 Head 里的 B (v3)。
	close(releaseOldCall)
	<-done
	require.NoError(t, oldErr)
	assert.Equal(t, model.AdtyTranslationFailed, oldResult.Status)
	assert.Equal(t, 2, oldResult.Version)

	var head model.AdtyTranslationHead
	require.NoError(t, db.Where("object_key = ?", "notice:race").First(&head).Error)
	assert.Equal(t, 3, head.Version)
	assert.Equal(t, longSourceB, head.Source)
	assert.Equal(t, model.AdtyTranslationFailed, head.Status)
	assert.Empty(t, head.English)
}

// ISS-R1-0146: 配额原子预占（含重试/备用模型）与并发调用计数、CostUSD 写入。
func TestAdtyTranslationAtomicQuotaAndUsageAccounting(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")
	db := prepareReferralDialectDB(t, "sqlite")

	_, err := SaveAdtyTranslationConfig(AdtyTranslationInput{
		Mode:        model.AdtyTranslationModeSet,
		ModelName:   "primary-model",
		BackupModel: "backup-model",
		DailyQuota:  1,
		RetryLimit:  2,
		Enabled:     true,
	})
	require.NoError(t, err)

	oldSleep := adtyTranslationSleep
	adtyTranslationSleep = func(time.Duration) {}
	t.Cleanup(func() { adtyTranslationSleep = oldSleep })

	var providerCalls int32
	oldTranslate := AdtyTranslateFunc
	AdtyTranslateFunc = func(modelName, _ string) (string, error) {
		atomic.AddInt32(&providerCalls, 1)
		if modelName == "primary-model" {
			return "", errors.New("primary failed")
		}
		return "Amount 12.50 USD", nil
	}
	t.Cleanup(func() { AdtyTranslateFunc = oldTranslate })

	// DailyQuota=1 且当前 Calls=0：主模型失败已用掉唯一配额，备用模型不得突破配额再打第 2 次。
	res, err := TranslateAdtyDynamic("notice:quota-1", "金额 12.50 USD", 0)
	require.NoError(t, err)
	assert.Equal(t, model.AdtyTranslationFailed, res.Status)
	assert.Equal(t, int32(1), atomic.LoadInt32(&providerCalls))

	usage, err := AdtyTranslationUsageToday()
	require.NoError(t, err)
	assert.Equal(t, 1, usage.Calls)
	assert.Equal(t, 1, usage.Failures)

	// 重置为 Calls=10，DailyQuota=100，验证并发 A/B 调用不会丢计数且写入 CostUSD。
	day := time.Now().UTC().Format("2006-01-02")
	require.NoError(t, db.Model(&model.AdtyTranslationUsage{}).Where("day = ?", day).Updates(map[string]any{
		"calls":    10,
		"failures": 0,
		"cost_usd": "0.001000",
	}).Error)
	_, err = SaveAdtyTranslationConfig(AdtyTranslationInput{
		Mode:       model.AdtyTranslationModeSet,
		ModelName:  "primary-model",
		DailyQuota: 100,
		Enabled:    true,
	})
	require.NoError(t, err)

	AdtyTranslateFunc = func(_, _ string) (string, error) {
		time.Sleep(10 * time.Millisecond)
		return "Amount 12.50 USD", nil
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = TranslateAdtyDynamic("notice:conc-a", "金额 12.50 USD", 0)
	}()
	go func() {
		defer wg.Done()
		_, _ = TranslateAdtyDynamic("notice:conc-b", "金额 12.50 USD", 0)
	}()
	wg.Wait()

	usageAfter, err := AdtyTranslationUsageToday()
	require.NoError(t, err)
	assert.Equal(t, 12, usageAfter.Calls)
	costDec, err := decimal.NewFromString(usageAfter.CostUSD)
	require.NoError(t, err)
	assert.True(t, costDec.GreaterThan(decimal.RequireFromString("0.001000")), "CostUSD must be incremented and written")
}

// ISS-R1-0147: 精确比对受保护的数值、数量、正负号、币种与百分号，拒绝子串误放行。
func TestAdtyTranslationKeepsFactsExactTokensCurrencyAndPercent(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")
	prepareReferralDialectDB(t, "sqlite")
	assert.False(t, adtyTranslationKeepsFacts("Amount12.50USD", "Amount112.50EUR"))
	assert.False(t, adtyTranslationKeepsFacts("Amount 12.50 USD", "Amount 112.50 USD"))
	assert.False(t, adtyTranslationKeepsFacts("Amount 12.50 USD", "Amount 12.50 EUR"))
	assert.False(t, adtyTranslationKeepsFacts("10%", "10USD"))
	assert.False(t, adtyTranslationKeepsFacts("10%", "10 USD"))
	assert.False(t, adtyTranslationKeepsFacts("12.50 USD", "-12.50 USD"))
	assert.False(t, adtyTranslationKeepsFacts("-12.50 USD", "12.50 USD"))
	assert.False(t, adtyTranslationKeepsFacts("12.50 USD", "12.50 USD and 12.50 USD"))
	assert.True(t, adtyTranslationKeepsFacts("折扣 10%，金额 12.50 USD", "Discount 10%, Amount 12.50 USD"))
}

// ISS-R1-0149 & ISS-R1-0152: 按 RetryLimit 循环与指数退避重试，失败落库任务并支持同对象同版本重试。
func TestAdtyTranslationRetryLoopBackoffAndSameVersionTaskRetry(t *testing.T) {
	t.Setenv("CRYPTO_SECRET", "synthetic-encryption-key-for-local-tests-only")
	prepareReferralDialectDB(t, "sqlite")

	_, err := SaveAdtyTranslationConfig(AdtyTranslationInput{
		Mode:        model.AdtyTranslationModeSet,
		ModelName:   "primary-m",
		BackupModel: "backup-m",
		RetryLimit:  3,
		Enabled:     true,
	})
	require.NoError(t, err)

	var sleeps []time.Duration
	oldSleep := adtyTranslationSleep
	adtyTranslationSleep = func(d time.Duration) {
		sleeps = append(sleeps, d)
	}
	t.Cleanup(func() { adtyTranslationSleep = oldSleep })

	var modelsCalled []string
	oldTranslate := AdtyTranslateFunc
	AdtyTranslateFunc = func(modelName, _ string) (string, error) {
		modelsCalled = append(modelsCalled, modelName)
		return "", errors.New("temporary upstream failure")
	}
	t.Cleanup(func() { AdtyTranslateFunc = oldTranslate })

	failed, err := TranslateAdtyDynamic("notice:retry-task", "金额 12.50 USD，订单 A123456，不退款", 0)
	require.NoError(t, err)
	assert.Equal(t, model.AdtyTranslationFailed, failed.Status)
	assert.Equal(t, 1, failed.Version)
	assert.Equal(t, []string{"primary-m", "backup-m", "backup-m", "backup-m"}, modelsCalled)
	require.Len(t, sleeps, 3)
	assert.True(t, sleeps[0] > 0 && sleeps[1] >= sleeps[0] && sleeps[2] >= sleeps[1])

	tasks, err := ListAdtyTranslationTasks(model.AdtyTranslationFailed)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "notice:retry-task", tasks[0].ObjectKey)
	assert.Equal(t, 1, tasks[0].Version)
	assert.NotZero(t, tasks[0].JobId)

	// 无备用模型时 RetryLimit=3 也必须对主模型重试 3 次（共 4 次调用）。
	_, err = SaveAdtyTranslationConfig(AdtyTranslationInput{
		Mode:       model.AdtyTranslationModeSet,
		ModelName:  "primary-m",
		RetryLimit: 3,
		Enabled:    true,
	})
	require.NoError(t, err)
	modelsCalled = nil
	_, err = TranslateAdtyDynamic("notice:retry-same-model", "金额 12.50 USD", 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"primary-m", "primary-m", "primary-m", "primary-m"}, modelsCalled)

	// 恢复上游后，对 notice:retry-task 执行同对象同版本（v1）重试。
	AdtyTranslateFunc = func(_, _ string) (string, error) {
		return "Amount 12.50 USD order A123456 is not refunded", nil
	}
	retried, err := RetryAdtyTranslationJob("notice:retry-task", 1)
	require.NoError(t, err)
	assert.Equal(t, model.AdtyTranslationReady, retried.Status)
	assert.Equal(t, 1, retried.Version)
	assert.Equal(t, "Amount 12.50 USD order A123456 is not refunded", retried.English)
}
