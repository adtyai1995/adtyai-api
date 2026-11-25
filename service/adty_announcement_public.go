package service

import (
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// AdtyPublicAnnouncementLimit 沿用旧 option 的上限（旧校验写的就是 100 条）。
const AdtyPublicAnnouncementLimit = 100

// AdtyPublicAnnouncement 是公开面板（首页/仪表盘）吃的形状。
//
// 它**故意**保持和旧 `console_setting.announcements` 一致：那个面板读的是
// `/api/status`，字段是 content/publishDate/type/extra。换数据源不换形状，
// 前端的未登录可见性就不会因为这次迁移而改变——这正是「读路径分叉」的要点。
type AdtyPublicAnnouncement struct {
	Id            int    `json:"id"`
	Title         string `json:"title,omitempty"`
	TitleEn       string `json:"title_en,omitempty"`
	Content       string `json:"content"`
	ContentEn     string `json:"content_en,omitempty"`
	ContentFormat string `json:"content_format,omitempty"`
	PublishDate   string `json:"publishDate"`
	Type          string `json:"type"`
	Extra         string `json:"extra"`
}

// AdtyPublicAnnouncementsForRequest 按请求的站点读已发布公告，供 `/api/status` 用。
//
// 这条路是**匿名**的：不经过任何认证中间件，所以只能用 Host（站点中间件早就
// 放进上下文了）判断可见性，不依赖登录用户。分站架构下这就是对的判断依据。
func AdtyPublicAnnouncementsForRequest(c *gin.Context) []AdtyPublicAnnouncement {
	out := []AdtyPublicAnnouncement{}
	siteID := 0
	if site := AdtySiteForRequest(c); site != nil {
		siteID = site.Id
	}
	rows, err := model.ListAdtyNotificationsForSite(siteID, AdtyPublicAnnouncementLimit)
	if err != nil {
		// 公告读失败不能让整个 /status 挂掉：站点开关、支付方式都在这份响应里。
		common.SysError(common.LogText("list public announcements for site %d failed: %s", siteID, err.Error()))
		return out
	}
	headByKey := map[string]model.AdtyTranslationHead{}
	if len(rows) > 0 && model.DB != nil && model.DB.Migrator().HasTable(&model.AdtyTranslationHead{}) {
		keys := make([]string, 0, len(rows)*2)
		for _, row := range rows {
			if row.Id > 0 {
				keys = append(keys, adtyNotificationTranslationKey(row.Id), adtyNotificationTitleTranslationKey(row.Id))
			}
		}
		if len(keys) > 0 {
			var heads []model.AdtyTranslationHead
			if err := model.DB.Where("object_key IN ? AND status = ?", keys, model.AdtyTranslationReady).Find(&heads).Error; err == nil {
				for _, head := range heads {
					headByKey[head.ObjectKey] = head
				}
			}
		}
	}
	for _, row := range rows {
		contentFormat := strings.ToLower(strings.TrimSpace(row.ContentFormat))
		if contentFormat == "" {
			contentFormat = "markdown"
		}
		isHTML := contentFormat == "html"
		rawContent := strings.TrimSpace(row.Content)
		content := rawContent
		if isHTML {
			content = SanitizeAdtyNotificationHTML(content)
		}
		contentEn := ""
		if head, ok := headByKey[adtyNotificationTranslationKey(row.Id)]; ok && strings.TrimSpace(head.English) != "" {
			currentSrc := normalizeAdtyNotificationSourceContentWithDB(model.DB, row.Id, rawContent)
			if adtyTranslationHeadMatchesSource(head, currentSrc) {
				eng := strings.TrimSpace(head.English)
				if isHTML {
					eng = SanitizeAdtyNotificationHTML(eng)
				}
				src := strings.TrimSpace(head.Source)
				// Guard against legacy rows where title was stored in notification:<id>
				// while the body itself was pure English.
				isLegacyTitleOnly := src != "" && src == strings.TrimSpace(row.Title) && !adtyContainsChinese(currentSrc)
				if !isLegacyTitleOnly && adtyContainsChinese(rawContent) {
					if src == "" {
						src = currentSrc
					}
					if isHTML {
						src = SanitizeAdtyNotificationHTML(src)
					}
					contentEn = eng
					if eng != "" && !strings.Contains(src, eng) {
						content = src + "\n\n" + eng
					} else if src != "" {
						content = src
					}
				}
			}
		}
		title := strings.TrimSpace(row.Title)
		titleEn := ""
		extra := row.Title
		if head, ok := headByKey[adtyNotificationTitleTranslationKey(row.Id)]; ok && strings.TrimSpace(head.English) != "" && adtyTranslationHeadMatchesSource(head, title) {
			titleEn = strings.TrimSpace(head.English)
			if title != "" && adtyContainsChinese(title) && !strings.Contains(title, titleEn) {
				extra = title + " / " + titleEn
			}
		}
		out = append(out, AdtyPublicAnnouncement{
			Id:            row.Id,
			Title:         title,
			TitleEn:       titleEn,
			Content:       content,
			ContentEn:     contentEn,
			ContentFormat: contentFormat,
			PublishDate:   time.Unix(row.PublishedAt, 0).UTC().Format(time.RFC3339),
			Type:          adtyLegacyAnnouncementColor(row.Business),
			Extra:         extra,
		})
	}
	return out
}

// adtyLegacyAnnouncementColor 从 Business 里解出旧面板的**颜色**语义。
//
// 迁移时旧公告的 type（default/ongoing/success/warning/error）没有别的地方可放，
// 就存在 `Business="legacy:type=warning"` 里——那时是为了能回滚，现在多一个用处：
// 公开面板还按颜色渲染状态点，读回来才不会让所有旧公告都变成灰点。
//
// 新发布的公告 Business 存的是「关联业务」文本，不是这个前缀，走 default。
func adtyLegacyAnnouncementColor(business string) string {
	const prefix = "legacy:type="
	if len(business) <= len(prefix) || business[:len(prefix)] != prefix {
		return "default"
	}
	switch color := business[len(prefix):]; color {
	case "default", "ongoing", "success", "warning", "error":
		return color
	}
	return "default"
}
