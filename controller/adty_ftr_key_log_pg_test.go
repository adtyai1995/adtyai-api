package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// One US dollar is 500000 quota. These tests use that constant directly.
const ftrKeyUserQuota = 500000

type ftrKeyFixture struct {
	db    *gorm.DB
	owner model.User
	other model.User
	token *model.Token
}

func newFTRKeyFixture(t *testing.T, name string, remain int, unlimited bool) ftrKeyFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })

	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.AuditIntent{}, &model.AuditLog{}, &model.Log{}, &model.AdtyGroup{}))

	owner := model.User{
		Username: name + "-owner", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Quota: ftrKeyUserQuota, Group: "default",
	}
	other := model.User{
		Username: name + "-other", Password: "hashed-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Quota: ftrKeyUserQuota, Group: "default",
	}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&other).Error)
	token := seedToken(t, db, owner.Id, name+"-key", name+"-secret-key")
	require.NoError(t, db.Model(token).Updates(map[string]any{
		"remain_quota": remain, "unlimited_quota": unlimited, "expired_time": int64(-1), "group": "default", "status": common.TokenStatusEnabled,
	}).Error)
	require.NoError(t, db.First(token, token.Id).Error)
	return ftrKeyFixture{db: db, owner: owner, other: other, token: token}
}

func (f ftrKeyFixture) call(t *testing.T, user model.User, method, target string, body map[string]any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	ctx, rec := newAuthenticatedContext(t, method, target, body, user.Id)
	ctx.Set("role", user.Role)
	ctx.Set("username", user.Username)
	return ctx, rec
}

func (f ftrKeyFixture) assertUserQuotaUnchanged(t *testing.T) {
	t.Helper()
	var quota int
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.owner.Id).Select("quota").Scan(&quota).Error)
	assert.Equal(t, ftrKeyUserQuota, quota, "key metadata must not change the owner wallet")
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.other.Id).Select("quota").Scan(&quota).Error)
	assert.Equal(t, ftrKeyUserQuota, quota, "a rejected foreign call must not change the other wallet")
}

func TestFTRADTY0403EditKeyNamePG(t *testing.T) {
	f := newFTRKeyFixture(t, "ftr-0403", 100, true)
	foreignCtx, foreignRec := f.call(t, f.other, http.MethodPut, "/api/token/", map[string]any{
		"id": f.token.Id, "name": "stolen-name", "expired_time": -1, "unlimited_quota": true, "group": "default",
	})
	UpdateToken(foreignCtx)
	foreignResp := decodeAPIResponse(t, foreignRec)
	assert.False(t, foreignResp.Success, foreignResp.Message)
	var stored model.Token
	require.NoError(t, f.db.First(&stored, f.token.Id).Error)
	assert.Equal(t, f.token.Name, stored.Name)
	assert.Equal(t, f.owner.Id, stored.UserId)

	ctx, rec := f.call(t, f.owner, http.MethodPut, "/api/token/", map[string]any{
		"id": f.token.Id, "name": "renamed-key", "expired_time": -1, "unlimited_quota": true, "group": "default",
	})
	UpdateToken(ctx)
	resp := decodeAPIResponse(t, rec)
	require.True(t, resp.Success, resp.Message)
	require.NoError(t, f.db.First(&stored, f.token.Id).Error)
	assert.Equal(t, "renamed-key", stored.Name)
	assert.Equal(t, 100, stored.RemainQuota)
	assert.Equal(t, f.owner.Id, stored.UserId)
	assert.Equal(t, common.RoleCommonUser, f.owner.Role)
	f.assertUserQuotaUnchanged(t)
}

func TestFTRADTY0404DeleteAPIKeyPG(t *testing.T) {
	f := newFTRKeyFixture(t, "ftr-0404", 100, true)
	foreignCtx, foreignRec := f.call(t, f.other, http.MethodDelete, "/api/token/"+strconv.Itoa(f.token.Id), nil)
	foreignCtx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(f.token.Id)}}
	DeleteToken(foreignCtx)
	assert.False(t, decodeAPIResponse(t, foreignRec).Success)
	var still model.Token
	require.NoError(t, f.db.First(&still, f.token.Id).Error)
	assert.False(t, still.DeletedAt.Valid)

	ctx, rec := f.call(t, f.owner, http.MethodDelete, "/api/token/"+strconv.Itoa(f.token.Id), nil)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(f.token.Id)}}
	DeleteToken(ctx)
	resp := decodeAPIResponse(t, rec)
	require.True(t, resp.Success, resp.Message)
	err := f.db.First(&still, f.token.Id).Error
	require.Error(t, err)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.NoError(t, f.db.Unscoped().First(&still, f.token.Id).Error)
	assert.True(t, still.DeletedAt.Valid)
	assert.Equal(t, f.owner.Id, still.UserId)
	f.assertUserQuotaUnchanged(t)
}

func TestFTRADTY0405KeyStatusTogglePG(t *testing.T) {
	f := newFTRKeyFixture(t, "ftr-0405", 100, true)
	ctx, rec := f.call(t, f.owner, http.MethodPut, "/api/token/?status_only=true", map[string]any{
		"id": f.token.Id, "status": common.TokenStatusDisabled,
	})
	UpdateToken(ctx)
	require.True(t, decodeAPIResponse(t, rec).Success, rec.Body.String())
	var stored model.Token
	require.NoError(t, f.db.First(&stored, f.token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, stored.Status)
	assert.Equal(t, f.token.Name, stored.Name)
	assert.Equal(t, 100, stored.RemainQuota)

	foreignCtx, foreignRec := f.call(t, f.other, http.MethodPut, "/api/token/?status_only=true", map[string]any{
		"id": f.token.Id, "status": common.TokenStatusEnabled,
	})
	UpdateToken(foreignCtx)
	assert.False(t, decodeAPIResponse(t, foreignRec).Success)
	require.NoError(t, f.db.First(&stored, f.token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, stored.Status)
	f.assertUserQuotaUnchanged(t)
}

func TestFTRADTY0406KeyQuotaConfigPG(t *testing.T) {
	f := newFTRKeyFixture(t, "ftr-0406", 100, false)
	foreignCtx, foreignRec := f.call(t, f.other, http.MethodPut, "/api/token/", map[string]any{
		"id": f.token.Id, "name": f.token.Name, "expired_time": -1, "remain_quota": 250, "unlimited_quota": false, "group": "default",
	})
	UpdateToken(foreignCtx)
	assert.False(t, decodeAPIResponse(t, foreignRec).Success)
	var stored model.Token
	require.NoError(t, f.db.First(&stored, f.token.Id).Error)
	assert.Equal(t, 100, stored.RemainQuota)

	ctx, rec := f.call(t, f.owner, http.MethodPut, "/api/token/", map[string]any{
		"id": f.token.Id, "name": f.token.Name, "expired_time": -1, "remain_quota": 250, "unlimited_quota": false, "group": "default",
	})
	UpdateToken(ctx)
	require.True(t, decodeAPIResponse(t, rec).Success, rec.Body.String())
	require.NoError(t, f.db.First(&stored, f.token.Id).Error)
	assert.Equal(t, 250, stored.RemainQuota)
	assert.Equal(t, f.owner.Id, stored.UserId)
	f.assertUserQuotaUnchanged(t)
}

func TestFTRADTY0407KeyExpiryConfigPG(t *testing.T) {
	f := newFTRKeyFixture(t, "ftr-0407", 100, true)
	const expiry int64 = 1893456000
	ctx, rec := f.call(t, f.owner, http.MethodPut, "/api/token/", map[string]any{
		"id": f.token.Id, "name": f.token.Name, "expired_time": expiry, "unlimited_quota": true, "group": "default",
	})
	UpdateToken(ctx)
	require.True(t, decodeAPIResponse(t, rec).Success, rec.Body.String())
	var stored model.Token
	require.NoError(t, f.db.First(&stored, f.token.Id).Error)
	assert.Equal(t, expiry, stored.ExpiredTime)
	assert.Equal(t, 100, stored.RemainQuota)

	foreignCtx, foreignRec := f.call(t, f.other, http.MethodPut, "/api/token/", map[string]any{
		"id": f.token.Id, "name": f.token.Name, "expired_time": int64(-1), "unlimited_quota": true, "group": "default",
	})
	UpdateToken(foreignCtx)
	assert.False(t, decodeAPIResponse(t, foreignRec).Success)
	require.NoError(t, f.db.First(&stored, f.token.Id).Error)
	assert.Equal(t, expiry, stored.ExpiredTime)
	f.assertUserQuotaUnchanged(t)
}

func TestFTRADTY0409KeyGroupPermissionPG(t *testing.T) {
	f := newFTRKeyFixture(t, "ftr-0409", 100, true)
	require.NoError(t, f.db.Create(&model.AdtyGroup{
		ID: "vip", Names: model.AdtyLangText{"en": "VIP", "zh-CN": "贵宾"}, Descs: model.AdtyLangText{"en": "vip", "zh-CN": "贵宾"},
	}).Error)
	require.NoError(t, f.db.Create(&model.AdtyGroup{
		ID: "gone", Names: model.AdtyLangText{"en": "Gone", "zh-CN": "已删"}, Descs: model.AdtyLangText{"en": "gone", "zh-CN": "已删"},
	}).Error)
	require.NoError(t, f.db.Delete(&model.AdtyGroup{}, "id = ?", "gone").Error)

	rejected, rejectedRec := f.call(t, f.owner, http.MethodPut, "/api/token/", map[string]any{
		"id": f.token.Id, "name": f.token.Name, "expired_time": -1, "unlimited_quota": true, "group": "gone",
	})
	UpdateToken(rejected)
	assert.False(t, decodeAPIResponse(t, rejectedRec).Success, rejectedRec.Body.String())
	var stored model.Token
	require.NoError(t, f.db.First(&stored, f.token.Id).Error)
	assert.Equal(t, "default", stored.Group)

	foreignCtx, foreignRec := f.call(t, f.other, http.MethodPut, "/api/token/", map[string]any{
		"id": f.token.Id, "name": f.token.Name, "expired_time": -1, "unlimited_quota": true, "group": "vip",
	})
	UpdateToken(foreignCtx)
	assert.False(t, decodeAPIResponse(t, foreignRec).Success)
	require.NoError(t, f.db.First(&stored, f.token.Id).Error)
	assert.Equal(t, "default", stored.Group)

	ctx, rec := f.call(t, f.owner, http.MethodPut, "/api/token/", map[string]any{
		"id": f.token.Id, "name": f.token.Name, "expired_time": -1, "unlimited_quota": true, "group": "vip",
	})
	UpdateToken(ctx)
	require.True(t, decodeAPIResponse(t, rec).Success, rec.Body.String())
	require.NoError(t, f.db.First(&stored, f.token.Id).Error)
	assert.Equal(t, "vip", stored.Group)
	assert.Equal(t, f.owner.Id, stored.UserId)
	f.assertUserQuotaUnchanged(t)
}

func TestFTRADTY0303BalanceChangeLogQueryPG(t *testing.T) {
	f := newFTRKeyFixture(t, "ftr-0303", 100, true)
	now := common.GetTimestamp()
	own := model.Log{
		UserId: f.owner.Id, Username: f.owner.Username, CreatedAt: now, Type: model.LogTypeTopup,
		Content: "topup 1 usd", Quota: ftrKeyUserQuota, TokenName: f.token.Name,
	}
	other := model.Log{
		UserId: f.other.Id, Username: f.other.Username, CreatedAt: now, Type: model.LogTypeTopup,
		Content: "other topup", Quota: 250000, TokenName: "other-key",
	}
	require.NoError(t, f.db.Create(&own).Error)
	require.NoError(t, f.db.Create(&other).Error)

	ctx, rec := f.call(t, f.owner, http.MethodGet, "/api/log/self?type=1&p=1&page_size=10", nil)
	ctx.Set("role", common.RoleCommonUser)
	GetUserLogs(ctx)
	body := rec.Body.String()
	require.Contains(t, body, `"success":true`, body)
	assert.Contains(t, body, "topup 1 usd")
	assert.NotContains(t, body, "other topup")
	assert.Contains(t, body, strconv.Itoa(ftrKeyUserQuota))

	var quota int
	require.NoError(t, f.db.Model(&model.User{}).Where("id = ?", f.owner.Id).Select("quota").Scan(&quota).Error)
	assert.Equal(t, ftrKeyUserQuota, quota, "reading balance logs must not credit the recorded amount again")
	var rows int64
	require.NoError(t, f.db.Model(&model.Log{}).Where("user_id = ?", f.owner.Id).Count(&rows).Error)
	assert.EqualValues(t, 1, rows)
}
