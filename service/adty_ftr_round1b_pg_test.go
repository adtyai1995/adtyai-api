package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ftrRound1bDB(t *testing.T, extra ...any) *gorm.DB {
	t.Helper()
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.AdtyBalanceLedger{}))
	if len(extra) > 0 {
		require.NoError(t, db.AutoMigrate(extra...))
	}
	return db
}

func ftrRound1bUser(t *testing.T, db *gorm.DB, name string, role int) model.User {
	t.Helper()
	user := model.User{
		Username:    name,
		Password:    "hashed-password",
		Role:        role,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		Email:       name + "@example.test",
		AuthVersion: 1,
		Quota:       500000,
	}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", 500000).Error)
	var stored model.User
	require.NoError(t, db.Select("id", "role", "quota", "site_id", "group").First(&stored, user.Id).Error)
	require.Equal(t, role, stored.Role)
	require.Equal(t, 500000, stored.Quota)
	user.Role = stored.Role
	user.Quota = stored.Quota
	user.SiteId = stored.SiteId
	user.Group = stored.Group
	return user
}

func ftrRound1bNoMoney(t *testing.T, db *gorm.DB, userID, role int) {
	t.Helper()
	var stored model.User
	require.NoError(t, db.Select("id", "role", "quota").First(&stored, userID).Error)
	assert.Equal(t, role, stored.Role)
	assert.Equal(t, 500000, stored.Quota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
}

// SealAdtyPurposeSecret reads CRYPTO_SECRET. common.CryptoSecret is test setup and is restored.
func ftrRound1bSeal(t *testing.T) {
	t.Helper()
	const secret = "synthetic-encryption-key-for-local-tests-only"
	previous := common.CryptoSecret
	common.CryptoSecret = secret
	t.Cleanup(func() { common.CryptoSecret = previous })
	t.Setenv("CRYPTO_SECRET", secret)
}

// ADTY-19-01: root saves the main-site platform translation config.
func TestFTRADTY1901SaveTranslationConfigPG(t *testing.T) {
	db := ftrRound1bDB(t, &model.AdtyTranslationConfig{})
	root := ftrRound1bUser(t, db, "ftr1901root", common.RoleRootUser)
	require.Equal(t, common.RoleRootUser, root.Role)

	saved, err := SaveAdtyTranslationConfig(AdtyTranslationInput{
		Mode:        model.AdtyTranslationModeSet,
		ModelName:   "ftr-1901-translator",
		Enabled:     true,
		DailyQuota:  10,
		MaxChars:    1000,
		RetryLimit:  1,
		Concurrency: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, model.AdtyTranslationModeSet, saved.Mode)
	assert.Equal(t, "ftr-1901-translator", saved.ModelName)

	var stored model.AdtyTranslationConfig
	require.NoError(t, db.First(&stored, 1).Error)
	assert.Equal(t, 1, stored.Id)
	assert.Equal(t, model.AdtyTranslationModeSet, stored.Mode)
	assert.Equal(t, "platform", stored.Mode)
	assert.Equal(t, "ftr-1901-translator", stored.ModelName)
	assert.True(t, stored.Enabled)
	assert.Equal(t, 10, stored.DailyQuota)
	assert.Equal(t, 1000, stored.MaxChars)
	assert.Equal(t, 1, stored.RetryLimit)
	assert.Equal(t, 1, stored.Concurrency)
	ftrRound1bNoMoney(t, db, root.Id, common.RoleRootUser)
}

// ADTY-19-02: platform mode stores the selected model and its backup.
func TestFTRADTY1902SelectTranslationModelPG(t *testing.T) {
	db := ftrRound1bDB(t, &model.AdtyTranslationConfig{})
	root := ftrRound1bUser(t, db, "ftr1902root", common.RoleRootUser)
	require.Equal(t, common.RoleRootUser, root.Role)

	saved, err := SaveAdtyTranslationConfig(AdtyTranslationInput{
		Mode:        model.AdtyTranslationModeSet,
		ModelName:   "ftr-1902-primary",
		BackupModel: "ftr-1902-backup",
		Enabled:     true,
		DailyQuota:  10,
		MaxChars:    1000,
		RetryLimit:  1,
		Concurrency: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, "ftr-1902-primary", saved.ModelName)
	assert.Equal(t, "ftr-1902-backup", saved.BackupModel)
	require.NotEmpty(t, saved.BackupModel)

	var stored model.AdtyTranslationConfig
	require.NoError(t, db.First(&stored, 1).Error)
	assert.Equal(t, model.AdtyTranslationModeSet, stored.Mode)
	assert.Equal(t, "ftr-1902-primary", stored.ModelName)
	assert.Equal(t, "ftr-1902-backup", stored.BackupModel)
	require.NotEmpty(t, stored.BackupModel)
	assert.NotEqual(t, stored.ModelName, stored.BackupModel)
	ftrRound1bNoMoney(t, db, root.Id, common.RoleRootUser)
}

// ADTY-18-08: root saves the platform user notification bot. GetMe may fail; a matching token is still stored.
func TestFTRADTY1808SaveUserTelegramBotPG(t *testing.T) {
	ftrRound1bSeal(t)
	db := ftrRound1bDB(t, &model.AdtyTelegramBot{}, &model.AdtyTelegramBinding{}, &model.AdtyTelegramDelivery{})
	root := ftrRound1bUser(t, db, "ftr1808root", common.RoleRootUser)
	require.Equal(t, common.RoleRootUser, root.Role)

	const token = "123456789:abcdefghijklmnopqrstuvwxyz"
	saved, err := SaveAdtyTelegramBot(model.AdtyTelegramPurposeUser, token, true)
	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, model.AdtyTelegramPurposeUser, saved.View.Purpose)
	assert.Equal(t, int64(123456789), saved.View.BotId)
	assert.True(t, saved.View.Enabled)

	var stored model.AdtyTelegramBot
	require.NoError(t, db.Where("purpose = ?", model.AdtyTelegramPurposeUser).First(&stored).Error)
	assert.Equal(t, "user", stored.Purpose)
	assert.True(t, stored.Enabled)
	assert.Equal(t, int64(123456789), stored.BotId)
	assert.NotEmpty(t, stored.TokenCipher)
	var adminRows int64
	require.NoError(t, db.Model(&model.AdtyTelegramBot{}).Where("purpose = ?", model.AdtyTelegramPurposeAdmin).Count(&adminRows).Error)
	assert.Zero(t, adminRows)
	ftrRound1bNoMoney(t, db, root.Id, common.RoleRootUser)
}

// ADTY-18-09: root saves the site-admin notification bot in this test's own schema.
func TestFTRADTY1809SaveAdminTelegramBotPG(t *testing.T) {
	ftrRound1bSeal(t)
	db := ftrRound1bDB(t, &model.AdtyTelegramBot{}, &model.AdtyTelegramBinding{}, &model.AdtyTelegramDelivery{})
	root := ftrRound1bUser(t, db, "ftr1809root", common.RoleRootUser)
	require.Equal(t, common.RoleRootUser, root.Role)

	const token = "223456789:abcdefghijklmnopqrstuvwxyz"
	saved, err := SaveAdtyTelegramBot(model.AdtyTelegramPurposeAdmin, token, true)
	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, model.AdtyTelegramPurposeAdmin, saved.View.Purpose)
	assert.Equal(t, int64(223456789), saved.View.BotId)
	assert.True(t, saved.View.Enabled)

	var rows []model.AdtyTelegramBot
	require.NoError(t, db.Order("id").Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, model.AdtyTelegramPurposeAdmin, rows[0].Purpose)
	assert.Equal(t, "admin", rows[0].Purpose)
	assert.True(t, rows[0].Enabled)
	assert.Equal(t, int64(223456789), rows[0].BotId)
	assert.NotEqual(t, model.AdtyTelegramPurposeUser, rows[0].Purpose)
	ftrRound1bNoMoney(t, db, root.Id, common.RoleRootUser)
}

// ADTY-20-01: a common user request sample is stored and listed for that caller's group.
func TestFTRADTY2001RecordGroupHealthPG(t *testing.T) {
	db := ftrRound1bDB(t, &model.AdtyGroupHealthSample{}, &model.AdtyGroupHealthState{}, &model.AdtyGroupHealthAlert{})
	user := ftrRound1bUser(t, db, "ftr2001user", common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, user.Role)
	require.Equal(t, 1, user.Role)
	require.Equal(t, 0, user.SiteId)
	require.Equal(t, "default", user.Group)

	now := time.Now()
	require.NoError(t, RecordAdtyGroupHealth(0, "default", "ftr-2001-model", model.AdtyGroupHealthRequest, model.AdtyGroupHealthSuccess, now))

	var samples []model.AdtyGroupHealthSample
	require.NoError(t, db.Find(&samples).Error)
	require.Len(t, samples, 1)
	assert.Equal(t, 0, samples[0].SiteId)
	assert.Equal(t, "default", samples[0].GroupName)
	assert.Equal(t, "ftr-2001-model", samples[0].ModelName)
	assert.Equal(t, model.AdtyGroupHealthRequest, samples[0].Kind)
	assert.Equal(t, "request", samples[0].Kind)
	assert.Equal(t, model.AdtyGroupHealthSuccess, samples[0].Outcome)
	assert.Equal(t, "success", samples[0].Outcome)

	var state model.AdtyGroupHealthState
	require.NoError(t, db.First(&state, 1).Error)
	assert.Equal(t, 1, state.Id)
	assert.Equal(t, now.Unix(), state.HeartbeatAt)

	views, err := ListAdtyGroupHealthForUser(user.Id, user.Group, "ftr-2001-model", nil, now)
	require.NoError(t, err)
	var matched *AdtyGroupHealthView
	for i := range views {
		if views[i].Group == "default" {
			matched = &views[i]
			break
		}
	}
	require.NotNil(t, matched, "main-site user site_id 0 must see the site 0 sample")
	assert.Equal(t, "default", matched.Group)
	assert.Equal(t, 1, matched.RequestSamples)
	assert.Equal(t, "platform", matched.Scope)
	assert.Equal(t, model.AdtyGroupHealthOK, matched.Status)
	require.NotNil(t, matched.RequestSuccessRate)
	assert.InDelta(t, 1, *matched.RequestSuccessRate, 0.001)
	ftrRound1bNoMoney(t, db, user.Id, common.RoleCommonUser)
}

// ADTY-20-08: a common user uploads a tiny PNG onto their own ticket. Sanitizing re-encodes PNG and does not dial out.
func TestFTRADTY2008UploadTicketImagePG(t *testing.T) {
	db := ftrRound1bDB(t,
		&model.AdtySite{},
		&model.AdtySiteStaff{},
		&model.AdtyTeamMember{},
		&model.AdtyTicket{},
		&model.AdtyTicketMessage{},
		&model.AdtyTicketEvent{},
		&model.AdtyTicketImage{},
		&model.AdtyTicketPolicy{},
	)
	dir := t.TempDir()
	restore := SetAdtyTicketImageDirForTest(dir)
	t.Cleanup(restore)

	user := ftrRound1bUser(t, db, "ftr2008user", common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, user.Role)
	require.Equal(t, 1, user.Role)

	created, err := CreateAdtyTicket(user.Id, AdtyTicketInput{
		Type:        "other",
		Title:       "Need help with a screenshot",
		Body:        "Please look at this image",
		Idempotency: "ftr-2008-image",
	})
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, user.Id, created.UserId)

	saved, err := AddAdtyTicketImage(user.Id, created.Id, model.AdtyTicketVisibilityPublic, tinyPNG(t))
	require.NoError(t, err)
	require.NotNil(t, saved)

	var stored model.AdtyTicketImage
	require.NoError(t, db.First(&stored, saved.Id).Error)
	assert.Equal(t, created.Id, stored.TicketId)
	assert.Equal(t, model.AdtyTicketVisibilityPublic, stored.Visibility)
	assert.Equal(t, "public", stored.Visibility)
	assert.Equal(t, "image/png", stored.Mime)
	assert.Equal(t, 2, stored.Width)
	assert.Equal(t, 2, stored.Height)
	assert.Positive(t, stored.Size)
	assert.NotEmpty(t, stored.Storage)
	info, statErr := os.Stat(filepath.Join(dir, stored.Storage))
	require.NoError(t, statErr)
	assert.Equal(t, int64(stored.Size), info.Size())

	var event model.AdtyTicketEvent
	require.NoError(t, db.Where("ticket_id = ? AND action = ?", created.Id, "image_upload").First(&event).Error)
	assert.Equal(t, user.Id, event.ActorId)
	assert.Equal(t, "image/png", event.Detail)
	ftrRound1bNoMoney(t, db, user.Id, common.RoleCommonUser)
}
