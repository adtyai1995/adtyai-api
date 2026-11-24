package model

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AdtyNotificationPublication keeps withdrawn publications and their original
// attribution. IssuerSiteId=0 is the Root publishing domain, never a recipient.
type AdtyNotificationPublication struct {
	Id              int    `json:"id" gorm:"primaryKey"`
	IssuerSiteId    int    `json:"issuer_site_id" gorm:"not null;index"`
	PublisherId     int    `json:"publisher_id" gorm:"not null"`
	PublisherName   string `json:"publisher_name" gorm:"type:varchar(80);not null"`
	Audience        string `json:"audience" gorm:"type:varchar(16);not null"`
	TargetAudiences string `json:"target_audiences" gorm:"type:text"`
	TargetSiteIDs   string `json:"target_site_ids" gorm:"type:text"`
	TargetSiteId    int    `json:"target_site_id" gorm:"not null;index"`
	TargetSiteName  string `json:"target_site_name" gorm:"type:varchar(255);not null"`
	Kind            string `json:"kind" gorm:"type:varchar(16);not null"`
	Title           string `json:"title" gorm:"type:varchar(120);not null"`
	Content         string `json:"content" gorm:"type:text;not null"`
	ContentFormat   string `json:"content_format" gorm:"type:varchar(16);default:markdown"`
	Business        string `json:"business" gorm:"type:varchar(200);not null"`
	Status          string `json:"status" gorm:"type:varchar(16);not null;index"`
	Revision        int64  `json:"revision" gorm:"not null"`
	ReadKey         string `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	PublishedAt     int64  `json:"published_at" gorm:"not null"`
	UpdatedAt       int64  `json:"updated_at" gorm:"not null"`
	UpdatedBy       int    `json:"updated_by" gorm:"not null"`
	// TelegramChannel 是这条公告要同步到的 TG 频道（`@name` 或 `-100…`）。
	// 空表示不同步。存在公告上而不是每次请求里带，编辑时才知道原来发到哪。
	TelegramChannel string `json:"telegram_channel" gorm:"type:varchar(128);not null;default:''"`
	// TelegramAt 是这条公告推送 TG 的时刻；0 表示没推过。
	//
	// 它只回答「推过没有」，不记「最近一次入队时间」：公告要推的是
	// 「新通知」，不是「持续编辑」。同一条公告改三次就是三条推送，
	// 用户会把它当骚扰——改错别字的收益远小于这个代价。
	// 所以非 0 之后就不再推，哪怕内容改了。
	TelegramAt int64 `json:"telegram_at" gorm:"not null;default:0"`
	// TelegramOutboxSeededAt records when Telegram outbox rows were first
	// enqueued for this publication. Once set, bounded outbox recovery skips
	// this publication and late-joining team members are never retroactively
	// pushed historical team announcements.
	TelegramOutboxSeededAt *int64 `json:"-" gorm:"index"`
}

// ParsedTargetAudiences decodes the JSON array stored in TargetAudiences.
// Empty/legacy rows return nil so callers fall back to Audience/TargetSiteId.
func (p AdtyNotificationPublication) ParsedTargetAudiences() []string {
	raw := strings.TrimSpace(p.TargetAudiences)
	if raw == "" {
		return nil
	}
	var list []string
	if err := common.UnmarshalJsonStr(raw, &list); err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		item = strings.TrimSpace(item)
		if item != "" && !slices.Contains(out, item) {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ParsedTargetSiteIDs decodes the positive site IDs stored in TargetSiteIDs.
func (p AdtyNotificationPublication) ParsedTargetSiteIDs() []int {
	raw := strings.TrimSpace(p.TargetSiteIDs)
	if raw == "" {
		return nil
	}
	var list []int
	if err := common.UnmarshalJsonStr(raw, &list); err != nil {
		return nil
	}
	out := make([]int, 0, len(list))
	for _, id := range list {
		if id > 0 && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// EffectiveTargetSiteIDs resolves the authoritative target site IDs for
// site_users/site_teams. Subsite-issued publications can only target their own issuer site.
func (p AdtyNotificationPublication) EffectiveTargetSiteIDs() []int {
	if p.IssuerSiteId > 0 {
		return []int{p.IssuerSiteId}
	}
	if ids := p.ParsedTargetSiteIDs(); len(ids) > 0 {
		return ids
	}
	if p.TargetSiteId > 0 {
		return []int{p.TargetSiteId}
	}
	return nil
}

// AdtyNotificationTargetSiteMatchSQL builds a cross-DB SQL predicate matching
// a targeted site ID inside the JSON integer array target_site_ids (or issuer_site_id
// for subsite-issued rows, with fallback to target_site_id when target_site_ids is empty).
func AdtyNotificationTargetSiteMatchSQL(siteID int, hasTargetSiteIDs bool) (string, []any) {
	if !hasTargetSiteIDs {
		return "issuer_site_id = ? OR (issuer_site_id = 0 AND target_site_id = ?)", []any{siteID, siteID}
	}
	idStr := strconv.Itoa(siteID)
	norm := "REPLACE(COALESCE(target_site_ids, ''), ' ', '')"
	sql := fmt.Sprintf(
		"issuer_site_id = ? OR (issuer_site_id = 0 AND (%s = ? OR %s LIKE ? OR %s LIKE ? OR %s LIKE ? OR (COALESCE(target_site_ids, '') IN ('', '[]') AND target_site_id = ?)))",
		norm, norm, norm, norm,
	)
	return sql, []any{
		siteID,
		"[" + idStr + "]",
		"[" + idStr + ",%",
		"%," + idStr + ",%",
		"%," + idStr + "]",
		siteID,
	}
}

var ErrAdtyNotificationConflict = common.NewMessage("This notification was changed elsewhere. Reload and try again")

// UpdateAdtyNotificationPublication uses a conditional write so a stale editor
// cannot overwrite a revision or restore a withdrawn publication.
func UpdateAdtyNotificationPublication(row *AdtyNotificationPublication, revision int64) error {
	return UpdateAdtyNotificationPublicationTx(DB, row, revision)
}

func UpdateAdtyNotificationPublicationTx(tx *gorm.DB, row *AdtyNotificationPublication, revision int64) error {
	updates := map[string]any{
		"audience": row.Audience, "target_site_id": row.TargetSiteId, "target_site_name": row.TargetSiteName,
		"kind": row.Kind, "title": row.Title, "content": row.Content, "business": row.Business,
		"status": row.Status, "revision": revision + 1, "updated_at": row.UpdatedAt, "updated_by": row.UpdatedBy,
		"read_key": row.ReadKey, "telegram_channel": row.TelegramChannel,
		// A draft gets its publish time when it is first published through this update;
		// later edits carry the stored value forward unchanged.
		"published_at": row.PublishedAt,
	}
	if tx.Migrator().HasColumn(&AdtyNotificationPublication{}, "target_audiences") {
		updates["target_audiences"] = row.TargetAudiences
	}
	if tx.Migrator().HasColumn(&AdtyNotificationPublication{}, "target_site_ids") {
		updates["target_site_ids"] = row.TargetSiteIDs
	}
	if tx.Migrator().HasColumn(&AdtyNotificationPublication{}, "content_format") {
		format := row.ContentFormat
		if format == "" {
			format = "markdown"
		}
		updates["content_format"] = format
	}
	result := tx.Model(&AdtyNotificationPublication{}).
		// 草稿也能编辑：`draft` 存在的意义就是「还没发出去、随便改」。
		// 已撤回的不能编辑——那不是编辑对象，是历史记录。
		Where("id = ? AND issuer_site_id = ? AND revision = ? AND status IN ?", row.Id, row.IssuerSiteId, revision, []string{"published", "draft"}).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrAdtyNotificationConflict
	}
	row.Revision = revision + 1
	return nil
}

// AdtyNotificationRead records the first read of a content version by one account
// on one site. Public browser history never becomes an account receipt.
type AdtyNotificationRead struct {
	UserId  int    `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	SiteId  int    `json:"site_id" gorm:"primaryKey;autoIncrement:false"`
	ReadKey string `json:"read_key" gorm:"type:char(64);primaryKey"`
	ReadAt  int64  `json:"read_at" gorm:"not null"`
}

func GetAdtyNotificationReadKeys(userID, siteID int, keys []string) ([]string, error) {
	read := []string{}
	if len(keys) == 0 {
		return read, nil
	}
	err := DB.Model(&AdtyNotificationRead{}).Where("user_id = ? AND site_id = ? AND read_key IN ?", userID, siteID, keys).
		Order("read_key").Pluck("read_key", &read).Error
	return read, err
}

func MarkAdtyNotificationsRead(userID, siteID int, keys []string, now int64) error {
	if len(keys) == 0 {
		return nil
	}
	rows := make([]AdtyNotificationRead, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, AdtyNotificationRead{UserId: userID, SiteId: siteID, ReadKey: key, ReadAt: now})
	}
	return DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

// ClaimAdtyNotificationTelegramSlot 原子地抢下这条公告的 TG 推送权。
//
// 条件写在 SQL 里（`telegram_at = 0`）而不是「先读再判断再写」：并发保存同一条
// 公告时只有一个能拿到 RowsAffected=1，另一个直接跳过。应用层 check-then-write
// 在这里会漏——两个请求都能读到 0。
func ClaimAdtyNotificationTelegramSlot(id int, at int64) (bool, error) {
	result := DB.Model(&AdtyNotificationPublication{}).
		Where("id = ? AND telegram_at = 0", id).
		Update("telegram_at", at)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// ReleaseAdtyNotificationTelegramSlot 在入队失败后把推送权还回去，
// 否则这条公告会被永久标成「推过」，而实际一条都没发出去。
func ReleaseAdtyNotificationTelegramSlot(id int) error {
	return DB.Model(&AdtyNotificationPublication{}).
		Where("id = ?", id).
		Update("telegram_at", 0).Error
}

// ImportLegacyAnnouncement 把一条旧 `console_setting.announcements` 落成一条
// IssuerSiteId=0、Audience=all_users 的发布记录，返回是否真的插入了一行。
//
// 幂等靠 ReadKey 的唯一索引，不靠「先查一遍再插」：重复导入会撞索引，
// OnConflict DoNothing 让它安静跳过，row 数不变。这条属性是可重跑的前提——
// 启动时每次都会调，不能每次多一批。
func ImportLegacyAnnouncement(row *AdtyNotificationPublication) (bool, error) {
	result := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(row)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// ListAdtyNotificationsForSite 返回某个站点上「已发布」的公告，供公开面板读。
//
// siteID=0 是主站。公开列表没有登录用户，只按站点判断：
//   - 旧平台受众（all_users / all_sites / all 且无 target_audiences）：主站和分站均可见；
//   - 旧 site 受众：仅指向自己的分站可见；
//   - 新 target_audiences：
//   - main_users 仅主站（siteID=0）可见，绝不出现在分站；
//   - site_users 仅在 target_site_ids 命中该分站（或该分站自发）时可见；
//   - all_site_owners / main_teams / site_teams / 旧 team / 旧 user 仅出现在登录收件箱，不进公开列表。
func ListAdtyNotificationsForSite(siteID int, limit int) ([]AdtyNotificationPublication, error) {
	items := []AdtyNotificationPublication{}
	// 调用方是 `/api/status`——那条路由在服务启动的最前面就会被访问。
	// 库还没初始化时这里是 nil，直接解引用会把整份 status 打成 panic，
	// 而公告只是 status 里的一个可选字段，不能拖垮站点开关和支付方式。
	if DB == nil {
		return items, nil
	}
	query := DB.Model(&AdtyNotificationPublication{}).Where("status = ?", "published")
	// 与登录收件箱的 platformAudiences 对齐。两处规则必须一起改。
	platformAudiences := []string{"all_users", "all_sites", "all"}
	hasTargetAudiences := DB.Migrator().HasColumn(&AdtyNotificationPublication{}, "target_audiences")
	hasTargetSiteIDs := DB.Migrator().HasColumn(&AdtyNotificationPublication{}, "target_site_ids")

	if !hasTargetAudiences {
		if siteID == 0 {
			query = query.Where("audience IN ?", platformAudiences)
		} else {
			query = query.Where("audience IN ? OR (audience = ? AND target_site_id = ?)",
				platformAudiences, "site", siteID)
		}
		err := query.Order("published_at DESC, id DESC").Limit(limit).Find(&items).Error
		return items, err
	}

	if siteID == 0 {
		query = query.Where(
			"(COALESCE(target_audiences, '') IN ('', '[]') AND audience IN ?) OR target_audiences LIKE ?",
			platformAudiences, `%"main_users"%`,
		)
	} else {
		siteMatchSQL, siteMatchArgs := AdtyNotificationTargetSiteMatchSQL(siteID, hasTargetSiteIDs)
		args := make([]any, 0, 4+len(siteMatchArgs))
		args = append(args, platformAudiences, "site", siteID, `%"site_users"%`)
		args = append(args, siteMatchArgs...)
		query = query.Where(
			"(COALESCE(target_audiences, '') IN ('', '[]') AND (audience IN ? OR (audience = ? AND target_site_id = ?))) OR (target_audiences LIKE ? AND ("+siteMatchSQL+"))",
			args...,
		)
	}
	err := query.Order("published_at DESC, id DESC").Limit(limit).Find(&items).Error
	return items, err
}

var _ = gorm.ErrRecordNotFound
