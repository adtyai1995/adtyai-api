package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ISS-R4-0105：注册链接完成入口是匿名的（router 只挂了 IP 级限流），口令校验
// 必须和登录共用同一套失败计数与锁定。否则一枚链接就是全平台已启用用户名的
// 密码预言机：猜错不计数、链接仍然 outstanding，可以一直猜下去。
func TestAdtyTeamRegistrationPasswordGuessesFeedTheLoginLock(t *testing.T) {
	db := prepareReferralDB(t)
	root := referralUser(t, db, teamRegName("rt"))
	require.NoError(t, db.Model(root).Update("role", common.RoleRootUser).Error)
	owner := referralUser(t, db, teamRegName("lk"))
	site := model.AdtySite{OwnerUserId: owner.Id, Subdomain: teamRegName("lks"), Status: model.AdtySiteStatusActive, CostFactor: decimalPtr("0.80")}
	require.NoError(t, db.Create(&site).Error)
	leader := referralUser(t, db, teamRegName("ld"))
	require.NoError(t, db.Model(leader).Update("site_id", site.Id).Error)
	leader.SiteId = site.Id
	hashTeamRegPassword(t, db, leader, "RightPass123")

	issued, err := issueAdtyTeamRegistration(root.Id, site.Id, time.Hour)
	require.NoError(t, err)
	common.ClearLoginFailures(leader.Username)
	t.Cleanup(func() { common.ClearLoginFailures(leader.Username) })
	require.False(t, common.LoginLocked(leader.Username))

	for i := 0; i < common.LoginLockLimit; i++ {
		_, err := CompleteAdtyTeamRegistration(issued.Code, leader.Username, "WrongPass123", "Lock Desk", nil)
		require.ErrorIs(t, err, ErrAdtyTeamInvalid)
	}
	assert.False(t, common.LoginLocked(leader.Username), "已有账号在核验口令前拒绝，不能变成密码预言机")

	_, err = CompleteAdtyTeamRegistration(issued.Code, leader.Username, "RightPass123", "Lock Desk", nil)
	require.ErrorIs(t, err, ErrAdtyTeamInvalid)
	var n int64
	require.NoError(t, db.Model(&model.AdtyTeam{}).Where("leader_user_id = ?", leader.Id).Count(&n).Error)
	assert.Zero(t, n)

	created, err := CompleteAdtyTeamRegistration(issued.Code, teamRegName("fresh"), "RightPass123", "Fresh Desk", nil)
	require.NoError(t, err)
	assert.NotEqual(t, leader.Id, created.Team.LeaderUserId)
}
