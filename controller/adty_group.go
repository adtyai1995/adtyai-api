package controller

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// adtyGroupDisplay 带全部语言的名字和说明，前端按「当前语言 → en → ID」挑。
type adtyGroupDisplay struct {
	ID      string             `json:"id"`
	Names   model.AdtyLangText `json:"names"`
	Descs   model.AdtyLangText `json:"descs"`
	Usable  bool               `json:"usable"`
	Visible bool               `json:"visible"`
}

type adtyGroupAdminView struct {
	ID    string              `json:"id"`
	Names model.AdtyLangText  `json:"names"`
	Descs model.AdtyLangText  `json:"descs"`
	Refs  model.AdtyGroupRefs `json:"refs"`
}

// GetAdtyGroupDisplay 返回分组显示名。
// 未登录：只返回未删除、并且访客可选（价格页按它显示）的分组。
// 登录：返回全部分组的名字，已删除的也返回，旧日志和旧密钥要显示；
// usable 表示本人现在能选，visible 表示没删除，两者给下拉框用。
func GetAdtyGroupDisplay(c *gin.Context) {
	userID := c.GetInt("id")
	loggedIn := userID > 0
	siteID := service.AdtyRequestSiteID(c)
	if siteID == 0 && loggedIn {
		if user, err := model.GetUserCache(userID); err == nil && user != nil && user.SiteId > 0 {
			siteID = user.SiteId
		}
	}
	groups, err := model.ListAdtyGroups(loggedIn)
	if err != nil {
		writeAdtyGroupError(c, err)
		return
	}
	usable := service.GetUserUsableGroups(c.GetString("group"))
	out := make([]adtyGroupDisplay, 0, len(groups))
	for _, group := range groups {
		if !service.SiteGroupAllowed(siteID, group.ID) {
			continue
		}
		_, isUsable := usable[group.ID]
		visible := !group.Deleted()
		if !loggedIn && !isUsable {
			continue
		}
		out = append(out, adtyGroupDisplay{
			ID:      group.ID,
			Names:   group.Names,
			Descs:   group.Descs,
			Usable:  isUsable && visible,
			Visible: visible,
		})
	}
	common.ApiSuccess(c, out)
}

// GetAdtyGroups 返回管理端分组列表（未删除）和引用数量。
func GetAdtyGroups(c *gin.Context) {
	groups, err := model.ListAdtyGroups(false)
	if err != nil {
		writeAdtyGroupError(c, err)
		return
	}
	out := make([]adtyGroupAdminView, 0, len(groups))
	for _, group := range groups {
		refs, err := model.CountAdtyGroupRefs(group.ID)
		if err != nil {
			writeAdtyGroupError(c, err)
			return
		}
		out = append(out, adtyGroupAdminView{
			ID:    group.ID,
			Names: group.Names,
			Descs: group.Descs,
			Refs:  refs,
		})
	}
	common.ApiSuccess(c, out)
}

// PostAdtyGroup 用一个名字和说明新建分组，并写创建审计。
func PostAdtyGroup(c *gin.Context) {
	var body struct {
		Name string `json:"name"`
		Desc string `json:"desc"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	created, err := model.CreateAdtyGroup(body.Name, body.Desc)
	if err != nil {
		writeAdtyGroupError(c, err)
		return
	}
	recordManageAudit(c, "adty.group.create", map[string]any{
		"id":   created.ID,
		"new":  created.Names,
		"desc": created.Descs,
	})
	common.ApiSuccess(c, created)
}

// UpdateAdtyGroup 改各语言的名字和说明。校验失败时不改原行，也不写审计。
func UpdateAdtyGroup(c *gin.Context) {
	id := c.Param("id")
	var body struct {
		Names model.AdtyLangText `json:"names"`
		Descs model.AdtyLangText `json:"descs"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := model.ValidateAdtyGroupTexts(body.Names, body.Descs); err != nil {
		writeAdtyGroupError(c, err)
		return
	}
	previous, err := model.GetAdtyGroup(id)
	if err != nil {
		writeAdtyGroupError(c, err)
		return
	}
	updated, err := model.UpdateAdtyGroupTexts(id, body.Names, body.Descs)
	if err != nil {
		writeAdtyGroupError(c, err)
		return
	}
	recordManageAudit(c, "adty.group.update", map[string]any{
		"id":       updated.ID,
		"old":      previous.Names,
		"new":      updated.Names,
		"old_desc": previous.Descs,
		"new_desc": updated.Descs,
	})
	common.ApiSuccess(c, updated)
}

// MigrateAdtyGroup 把一个分组的用户、密钥、渠道和分站全部改到另一个分组，并写审计。
func MigrateAdtyGroup(c *gin.Context) {
	id := c.Param("id")
	var body struct {
		To string `json:"to"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	moved, err := model.MigrateAdtyGroup(id, body.To)
	if err != nil {
		writeAdtyGroupError(c, err)
		return
	}
	recordManageAudit(c, "adty.group.migrate", map[string]any{
		"id":       id,
		"to":       body.To,
		"users":    moved.Users,
		"tokens":   moved.Tokens,
		"channels": moved.Channels,
		"sites":    moved.Sites,
	})
	common.ApiSuccess(c, moved)
}

// DeleteAdtyGroup 软删除分组（只要系统仍有其他未删除分组即可删除，含 default），
// 在同一事务内清理渠道、分站白名单、用户默认分组及全部 8 个分组配置项，提交后刷新缓存。
func DeleteAdtyGroup(c *gin.Context) {
	id := c.Param("id")
	if err := model.DeleteAdtyGroup(id); err != nil {
		writeAdtyGroupError(c, err)
		return
	}
	service.InvalidateAdtySiteCache()
	service.ResetAdtyModelProbeGroup(id)
	InvalidateUptimeStatusCache()
	model.InvalidatePricingCache()
	recordManageAudit(c, "adty.group.delete", map[string]any{"id": id})
	common.ApiSuccess(c, nil)
}

func writeAdtyGroupError(c *gin.Context, err error) {
	var message *common.Message
	switch {
	case errors.As(err, &message):
		common.ApiError(c, err)
	case errors.Is(err, model.ErrAdtyGroupNotFound):
		common.ApiErrorT(c, "Group not found")
	case errors.Is(err, model.ErrAdtyGroupTextRequired):
		common.ApiErrorT(c, "The English group name is required")
	case errors.Is(err, model.ErrAdtyGroupSameTarget):
		common.ApiErrorT(c, "Choose a different target group")
	default:
		common.SysError(common.LogText("adty group: %s", err.Error()))
		common.ApiErrorT(c, "Database error, please contact the administrator")
	}
}
