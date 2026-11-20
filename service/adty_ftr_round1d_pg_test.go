package service

import (
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ftrRound1dActor(t *testing.T, db *gorm.DB, id, role int) model.User {
	t.Helper()
	var stored model.User
	require.NoError(t, db.Select("id", "username", "role", "site_id", "staff_site_id", "group", "quota", "status").First(&stored, id).Error)
	require.Equal(t, role, stored.Role)
	require.Equal(t, 0, stored.SiteId)
	require.Equal(t, 0, stored.StaffSiteId)
	require.Equal(t, "default", stored.Group)
	require.Equal(t, common.UserStatusEnabled, stored.Status)
	return stored
}

func ftrRound1dPublishAllUsers(t *testing.T, root model.User, title string) *model.AdtyNotificationPublication {
	t.Helper()
	published, err := SaveAdtyNotificationPublication(root.Id, nil, 0, AdtyNotificationInput{
		Title:    title,
		Content:  "Main site notice body for " + title,
		Kind:     "platform",
		Audience: "all_users",
	}, false)
	require.NoError(t, err)
	require.NotNil(t, published)
	return published
}

func ftrRound1dStoredPublication(t *testing.T, db *gorm.DB, title string, publisherID int) model.AdtyNotificationPublication {
	t.Helper()
	var stored model.AdtyNotificationPublication
	require.NoError(t, db.Where("title = ? AND issuer_site_id = ?", title, 0).First(&stored).Error)
	assert.Equal(t, title, stored.Title)
	assert.Equal(t, "published", stored.Status)
	assert.Equal(t, 0, stored.IssuerSiteId)
	assert.Equal(t, publisherID, stored.PublisherId)
	assert.Equal(t, "all_users", stored.Audience)
	assert.Equal(t, "platform", stored.Kind)
	assert.NotEmpty(t, stored.ReadKey)
	assert.NotZero(t, stored.PublishedAt)
	return stored
}

func ftrRound1dNotice(t *testing.T, inbox AdtyNotificationInbox, title, readKey string) map[string]any {
	t.Helper()
	for _, item := range inbox.Announcements {
		if item["title"] == title {
			assert.Equal(t, readKey, item["read_key"])
			assert.Equal(t, "all_users", item["audience"])
			ftrRound1dAssertInt(t, 0, item["issuer_site_id"])
			return item
		}
	}
	t.Fatalf("notice %q is not visible", title)
	return nil
}

func ftrRound1dAssertInt(t *testing.T, want int, got any) {
	t.Helper()
	switch typed := got.(type) {
	case int:
		assert.Equal(t, want, typed)
	case int64:
		assert.Equal(t, int64(want), typed)
	case float64:
		assert.Equal(t, float64(want), typed)
	default:
		t.Fatalf("expected integer %d, got %T (%v)", want, got, got)
	}
}

func ftrRound1dMissingTable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "does not exist") || strings.Contains(msg, "42p01") || strings.Contains(msg, "no such table")
}

func ftrRound1dHide(t *testing.T, db *gorm.DB, userID int, key string) {
	t.Helper()
	err := HideAdtyNotificationForUser(userID, key)
	if ftrRound1dMissingTable(err) {
		require.NoError(t, db.AutoMigrate(&model.AuditLog{}, &model.AuditIntent{}))
		err = HideAdtyNotificationForUser(userID, key)
	}
	require.NoError(t, err)
}

func ftrRound1dDefaultHealth(t *testing.T, views []AdtyGroupHealthView) AdtyGroupHealthView {
	t.Helper()
	for _, view := range views {
		if view.Group == "default" {
			return view
		}
	}
	t.Fatal("default group health view missing")
	return AdtyGroupHealthView{}
}

func ftrRound1dRecordRequest(t *testing.T, db *gorm.DB, user model.User, modelName string, now time.Time) {
	t.Helper()
	require.Equal(t, common.RoleCommonUser, user.Role)
	require.Equal(t, 1, user.Role)
	require.Equal(t, 0, user.SiteId)
	require.Equal(t, "default", user.Group)
	ftrRound1PinQuota(t, db, user.Id)
	require.NoError(t, RecordAdtyGroupHealth(0, "default", modelName, model.AdtyGroupHealthRequest, model.AdtyGroupHealthSuccess, now))
}

// ADTY-18-01: root publishes a main-site platform notice for all users.
func TestFTRADTY1801GenerateNotificationPG(t *testing.T) {
	db := ftrRound1PG(t)
	root := ftrRound1User(t, db, "ftr1801root", common.RoleRootUser)
	root = ftrRound1dActor(t, db, root.Id, common.RoleRootUser)
	require.Equal(t, 100, root.Role)

	const title = "FTR1801 generated notice"
	ftrRound1PinQuota(t, db, root.Id)
	published := ftrRound1dPublishAllUsers(t, root, title)
	assert.Equal(t, "published", published.Status)
	assert.Equal(t, 0, published.IssuerSiteId)
	assert.Equal(t, root.Id, published.PublisherId)
	assert.Equal(t, root.Username, published.PublisherName)
	assert.Equal(t, "all_users", published.Audience)

	stored := ftrRound1dStoredPublication(t, db, title, root.Id)
	assert.Equal(t, root.Username, stored.PublisherName)
	assert.Equal(t, published.Id, stored.Id)
	ftrRound1AssertNoMoney(t, db, root.Id)
}

// ADTY-18-02: a published all-users notice is visible to common users on the main site.
func TestFTRADTY1802NotificationListPG(t *testing.T) {
	db := ftrRound1PG(t)
	root := ftrRound1User(t, db, "ftr1802root", common.RoleRootUser)
	user := ftrRound1User(t, db, "ftr1802user", common.RoleCommonUser)
	other := ftrRound1User(t, db, "ftr1802other", common.RoleCommonUser)
	root = ftrRound1dActor(t, db, root.Id, common.RoleRootUser)
	user = ftrRound1dActor(t, db, user.Id, common.RoleCommonUser)
	other = ftrRound1dActor(t, db, other.Id, common.RoleCommonUser)
	require.Equal(t, 100, root.Role)
	require.Equal(t, 1, user.Role)
	require.Equal(t, 1, other.Role)

	const title = "FTR1802 platform notice"
	ftrRound1PinQuota(t, db, root.Id)
	ftrRound1PinQuota(t, db, user.Id)
	ftrRound1PinQuota(t, db, other.Id)
	published := ftrRound1dPublishAllUsers(t, root, title)
	stored := ftrRound1dStoredPublication(t, db, title, root.Id)
	assert.Equal(t, published.ReadKey, stored.ReadKey)

	inbox, err := GetAdtyNotificationInbox(user.Id, nil, 0)
	require.NoError(t, err)
	ftrRound1dNotice(t, inbox, title, stored.ReadKey)
	otherInbox, err := GetAdtyNotificationInbox(other.Id, nil, 0)
	require.NoError(t, err)
	ftrRound1dNotice(t, otherInbox, title, stored.ReadKey)

	ftrRound1AssertNoMoney(t, db, root.Id)
	ftrRound1AssertNoMoney(t, db, user.Id)
	ftrRound1AssertNoMoney(t, db, other.Id)
}

// ADTY-18-04: hiding a notice writes one hide row for that user and leaves the publication.
func TestFTRADTY1804HideNotificationPG(t *testing.T) {
	db := ftrRound1PG(t)
	root := ftrRound1User(t, db, "ftr1804root", common.RoleRootUser)
	user := ftrRound1User(t, db, "ftr1804user", common.RoleCommonUser)
	other := ftrRound1User(t, db, "ftr1804other", common.RoleCommonUser)
	root = ftrRound1dActor(t, db, root.Id, common.RoleRootUser)
	user = ftrRound1dActor(t, db, user.Id, common.RoleCommonUser)
	other = ftrRound1dActor(t, db, other.Id, common.RoleCommonUser)
	require.Equal(t, 100, root.Role)
	require.Equal(t, 1, user.Role)
	require.Equal(t, 1, other.Role)

	const title = "FTR1804 hide notice"
	ftrRound1PinQuota(t, db, root.Id)
	ftrRound1PinQuota(t, db, user.Id)
	ftrRound1PinQuota(t, db, other.Id)
	published := ftrRound1dPublishAllUsers(t, root, title)
	stored := ftrRound1dStoredPublication(t, db, title, root.Id)
	require.Equal(t, published.ReadKey, stored.ReadKey)

	ftrRound1dHide(t, db, user.Id, stored.ReadKey)

	var hides []model.AdtyNotificationHide
	require.NoError(t, db.Where("user_id = ? AND key = ?", user.Id, stored.ReadKey).Find(&hides).Error)
	require.Len(t, hides, 1)
	assert.Equal(t, user.Id, hides[0].UserId)
	assert.Equal(t, stored.ReadKey, hides[0].Key)

	var otherHides int64
	require.NoError(t, db.Model(&model.AdtyNotificationHide{}).Where("user_id = ? AND key = ?", other.Id, stored.ReadKey).Count(&otherHides).Error)
	assert.Zero(t, otherHides)

	var still model.AdtyNotificationPublication
	require.NoError(t, db.First(&still, stored.Id).Error)
	assert.Equal(t, stored.Id, still.Id)
	assert.Equal(t, "published", still.Status)
	assert.Equal(t, 0, still.IssuerSiteId)
	assert.Equal(t, stored.ReadKey, still.ReadKey)
	assert.Equal(t, "all_users", still.Audience)

	hidden, err := GetAdtyNotificationInbox(user.Id, nil, 0)
	require.NoError(t, err)
	for _, item := range hidden.Announcements {
		assert.NotEqual(t, stored.ReadKey, item["read_key"])
	}
	visible, err := GetAdtyNotificationInbox(other.Id, nil, 0)
	require.NoError(t, err)
	ftrRound1dNotice(t, visible, title, stored.ReadKey)

	var audits []model.AuditLog
	require.NoError(t, db.Where("user_id = ? AND action = ? AND object_id = ?", user.Id, "adty.notification.hide", stored.ReadKey).Find(&audits).Error)
	require.Len(t, audits, 1)
	assert.Equal(t, common.RoleCommonUser, audits[0].ActorRole)
	assert.Equal(t, model.AuditResultSuccess, audits[0].Result)
	assert.True(t, audits[0].Success)
	require.NotNil(t, audits[0].SiteId)
	assert.Equal(t, 0, *audits[0].SiteId)

	ftrRound1AssertNoMoney(t, db, root.Id)
	ftrRound1AssertNoMoney(t, db, user.Id)
	ftrRound1AssertNoMoney(t, db, other.Id)
}

// ADTY-20-03: one successful request sample is a 100 percent request success rate.
func TestFTRADTY2003RequestSuccessRatePG(t *testing.T) {
	db := ftrRound1PG(t)
	user := ftrRound1User(t, db, "ftr2003user", common.RoleCommonUser)
	user = ftrRound1dActor(t, db, user.Id, common.RoleCommonUser)

	now := time.Now()
	const modelName = "ftr-2003-model"
	ftrRound1dRecordRequest(t, db, user, modelName, now)

	var samples []model.AdtyGroupHealthSample
	require.NoError(t, db.Where("site_id = ? AND group_name = ? AND model_name = ? AND kind = ?", 0, "default", modelName, model.AdtyGroupHealthRequest).Find(&samples).Error)
	require.Len(t, samples, 1)
	assert.Equal(t, 0, samples[0].SiteId)
	assert.Equal(t, "default", samples[0].GroupName)
	assert.Equal(t, model.AdtyGroupHealthRequest, samples[0].Kind)
	assert.Equal(t, model.AdtyGroupHealthSuccess, samples[0].Outcome)

	views, err := ListAdtyGroupHealthForUser(user.Id, user.Group, modelName, nil, now)
	require.NoError(t, err)
	matched := ftrRound1dDefaultHealth(t, views)
	assert.Equal(t, "default", matched.Group)
	assert.Equal(t, "platform", matched.Scope)
	assert.GreaterOrEqual(t, matched.RequestSamples, 1)
	require.NotNil(t, matched.RequestSuccessRate)
	assert.Equal(t, 1.0, *matched.RequestSuccessRate)
	ftrRound1AssertNoMoney(t, db, user.Id)
}

// ADTY-20-05: the request sample count and its non-zero sample time are listed.
func TestFTRADTY2005HealthSampleTimePG(t *testing.T) {
	db := ftrRound1PG(t)
	user := ftrRound1User(t, db, "ftr2005user", common.RoleCommonUser)
	user = ftrRound1dActor(t, db, user.Id, common.RoleCommonUser)

	now := time.Now()
	const modelName = "ftr-2005-model"
	ftrRound1dRecordRequest(t, db, user, modelName, now)

	var samples []model.AdtyGroupHealthSample
	require.NoError(t, db.Where("site_id = ? AND group_name = ? AND model_name = ? AND kind = ?", 0, "default", modelName, model.AdtyGroupHealthRequest).Find(&samples).Error)
	require.Len(t, samples, 1)
	assert.Equal(t, 0, samples[0].SiteId)
	assert.Equal(t, now.Unix(), samples[0].CreatedAt)
	assert.NotZero(t, samples[0].CreatedAt)

	views, err := ListAdtyGroupHealthForUser(user.Id, user.Group, modelName, nil, now)
	require.NoError(t, err)
	matched := ftrRound1dDefaultHealth(t, views)
	assert.Equal(t, "default", matched.Group)
	assert.Equal(t, len(samples), matched.RequestSamples)
	assert.Equal(t, 1, matched.RequestSamples)
	assert.NotZero(t, matched.UpdatedAt)
	assert.Equal(t, samples[0].CreatedAt, matched.UpdatedAt)
	ftrRound1AssertNoMoney(t, db, user.Id)
}
