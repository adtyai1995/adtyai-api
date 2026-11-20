package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ftrRound1PG(t *testing.T) *gorm.DB {
	t.Helper()
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.AdtySite{},
		&model.AdtySiteStaff{},
		&model.AdtyTeam{},
		&model.AdtyTeamMember{},
		&model.AdtyBalanceLedger{},
	))
	require.NoError(t, model.MigrateAdtyTables())
	return db
}

func ftrRound1User(t *testing.T, db *gorm.DB, name string, role int) model.User {
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
	return user
}

func ftrRound1PinQuota(t *testing.T, db *gorm.DB, userID int) {
	t.Helper()
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", userID).Update("quota", 500000).Error)
	var stored model.User
	require.NoError(t, db.Select("id", "quota").First(&stored, userID).Error)
	require.Equal(t, 500000, stored.Quota)
}

func ftrRound1AssertNoMoney(t *testing.T, db *gorm.DB, userID int) {
	t.Helper()
	var stored model.User
	require.NoError(t, db.Select("id", "quota").First(&stored, userID).Error)
	assert.Equal(t, 500000, stored.Quota)
	var ledgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Where("user_id = ?", userID).Count(&ledgers).Error)
	assert.Zero(t, ledgers)
	var allLedgers int64
	require.NoError(t, db.Model(&model.AdtyBalanceLedger{}).Count(&allLedgers).Error)
	assert.Zero(t, allLedgers)
}

// ADTY-20-07: a common user creates a support ticket. CreateAdtyTicket writes status new.
func TestFTRADTY2007CreateTicketPG(t *testing.T) {
	db := ftrRound1PG(t)
	user := ftrRound1User(t, db, "ftr2007user", common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, user.Role)
	require.Equal(t, 1, user.Role)

	ftrRound1PinQuota(t, db, user.Id)
	created, err := CreateAdtyTicket(user.Id, AdtyTicketInput{
		Type:        "other",
		Title:       "Need help with my account",
		Body:        "Please look at this request",
		Idempotency: "ftr-2007-create",
	})
	require.NoError(t, err)
	require.NotNil(t, created)

	var stored model.AdtyTicket
	require.NoError(t, db.First(&stored, created.Id).Error)
	assert.Equal(t, user.Id, stored.UserId)
	assert.Equal(t, user.SiteId, stored.SiteId)
	assert.Equal(t, model.AdtyTicketNew, stored.Status)
	assert.Equal(t, model.AdtyTicketNew, created.Status)

	var message model.AdtyTicketMessage
	require.NoError(t, db.Where("ticket_id = ?", stored.Id).First(&message).Error)
	assert.Equal(t, user.Id, message.AuthorId)
	assert.Equal(t, model.AdtyTicketVisibilityPublic, message.Visibility)

	ftrRound1AssertNoMoney(t, db, user.Id)
}

// ADTY-18-03: MarkAdtyNotificationInboxRead writes a receipt for the caller only.
func TestFTRADTY1803NotificationReadPG(t *testing.T) {
	db := ftrRound1PG(t)
	reader := ftrRound1User(t, db, "ftr1803reader", common.RoleCommonUser)
	other := ftrRound1User(t, db, "ftr1803other", common.RoleCommonUser)
	require.Equal(t, common.RoleCommonUser, reader.Role)
	require.Equal(t, 1, reader.Role)

	readKey := "ftr1803" + strings.Repeat("a", 64-len("ftr1803"))
	require.Len(t, readKey, 64)
	pub := model.AdtyNotificationPublication{
		IssuerSiteId:  0,
		PublisherId:   reader.Id,
		PublisherName: reader.Username,
		Audience:      "all_users",
		Kind:          "system",
		Title:         "Visible notice",
		Content:       "Please read this notice",
		Status:        "published",
		Revision:      1,
		ReadKey:       readKey,
		PublishedAt:   1,
		UpdatedAt:     1,
		UpdatedBy:     reader.Id,
	}
	require.NoError(t, db.Create(&pub).Error)

	var before int64
	require.NoError(t, db.Model(&model.AdtyNotificationRead{}).Where("read_key = ?", pub.ReadKey).Count(&before).Error)
	assert.Zero(t, before)
	inbox, err := GetAdtyNotificationInbox(reader.Id, nil, 0)
	require.NoError(t, err)
	assert.NotContains(t, inbox.ReadKeys, pub.ReadKey)
	assert.EqualValues(t, 1, inbox.UnreadPublications)

	ftrRound1PinQuota(t, db, reader.Id)
	ftrRound1PinQuota(t, db, other.Id)
	require.NoError(t, MarkAdtyNotificationInboxRead(reader.Id, nil, []string{pub.ReadKey}))

	var receipts []model.AdtyNotificationRead
	require.NoError(t, db.Where("read_key = ?", pub.ReadKey).Find(&receipts).Error)
	require.Len(t, receipts, 1)
	assert.Equal(t, reader.Id, receipts[0].UserId)
	assert.Equal(t, 0, receipts[0].SiteId)
	assert.NotZero(t, receipts[0].ReadAt)
	var otherReads int64
	require.NoError(t, db.Model(&model.AdtyNotificationRead{}).Where("user_id = ? AND read_key = ?", other.Id, pub.ReadKey).Count(&otherReads).Error)
	assert.Zero(t, otherReads)

	after, err := GetAdtyNotificationInbox(reader.Id, nil, 0)
	require.NoError(t, err)
	assert.Contains(t, after.ReadKeys, pub.ReadKey)
	assert.Zero(t, after.UnreadPublications)
	otherInbox, err := GetAdtyNotificationInbox(other.Id, nil, 0)
	require.NoError(t, err)
	assert.NotContains(t, otherInbox.ReadKeys, pub.ReadKey)
	assert.EqualValues(t, 1, otherInbox.UnreadPublications)

	ftrRound1AssertNoMoney(t, db, reader.Id)
	ftrRound1AssertNoMoney(t, db, other.Id)
}

// ADTY-18-06: Root publishes a main-site announcement through SaveAdtyNotificationPublication.
func TestFTRADTY1806PublishAnnouncementPG(t *testing.T) {
	db := ftrRound1PG(t)
	root := ftrRound1User(t, db, "ftr1806root", common.RoleRootUser)
	require.Equal(t, common.RoleRootUser, root.Role)

	const title = "FTR1806 main site notice"
	ftrRound1PinQuota(t, db, root.Id)
	published, err := SaveAdtyNotificationPublication(root.Id, nil, 0, AdtyNotificationInput{
		Title:    title,
		Content:  "The main site notice is now published.",
		Kind:     "platform",
		Audience: "all_users",
	}, false)
	require.NoError(t, err)
	require.NotNil(t, published)
	assert.Equal(t, title, published.Title)
	assert.Equal(t, "published", published.Status)
	assert.Equal(t, 0, published.IssuerSiteId)
	assert.Equal(t, root.Id, published.PublisherId)

	var stored model.AdtyNotificationPublication
	require.NoError(t, db.Where("title = ? AND issuer_site_id = ?", title, 0).First(&stored).Error)
	assert.Equal(t, title, stored.Title)
	assert.Equal(t, "published", stored.Status)
	assert.Equal(t, root.Id, stored.PublisherId)
	assert.Equal(t, "all_users", stored.Audience)
	assert.NotZero(t, stored.PublishedAt)

	ftrRound1AssertNoMoney(t, db, root.Id)
}
