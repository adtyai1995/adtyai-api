package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func filterDisabledStaffPermissions(perms []string) []string {
	if len(perms) == 0 {
		return perms
	}
	filtered := make([]string, 0, len(perms))
	for _, perm := range perms {
		if perm == "customers.balance_request" {
			continue
		}
		filtered = append(filtered, perm)
	}
	return filtered
}

func sanitizeAdtyStaffView(view service.AdtyStaffView) service.AdtyStaffView {
	view.Permissions = filterDisabledStaffPermissions(view.Permissions)
	return view
}

func stripDisabledStaffInputPermissions(input *service.AdtyStaffInput) {
	if input == nil || input.Permissions == nil {
		return
	}
	filtered := filterDisabledStaffPermissions(*input.Permissions)
	input.Permissions = &filtered
}

func GetAdtyOwnerStaff(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	page, offset, limit := adtyPage(c)
	query := model.DB.Model(&model.AdtySiteStaff{}).Where("site_id = ? AND owner_user_id = ? AND status <> ?", site.Id, c.GetInt("id"), "deleted")
	var total int64
	var staff []model.AdtySiteStaff
	if err := query.Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&staff).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	items := make([]service.AdtyStaffView, 0, len(staff))
	for _, member := range staff {
		items = append(items, sanitizeAdtyStaffView(service.AdtyStaffViewOf(member)))
	}
	page.SetTotal(int(total))
	page.SetItems(items)
	common.ApiSuccess(c, page)
}

func GetAdtyOwnerStaffAudits(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	page, offset, limit := adtyPage(c)
	query := model.DB.Model(&model.AdtySiteAudit{}).Where("site_id = ? AND action LIKE ?", site.Id, "staff.%")
	var total int64
	items := []model.AdtySiteAudit{}
	if err := query.Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	type staffAuditView struct {
		model.AdtySiteAudit
		StaffId          int    `json:"staff_id"`
		StaffUserId      int    `json:"staff_user_id,omitempty"`
		CustomerId       int    `json:"customer_id"`
		OperatorUsername string `json:"operator_username,omitempty"`
		StaffUsername    string `json:"staff_username,omitempty"`
		CustomerUsername string `json:"customer_username,omitempty"`
	}
	// R5-040: Collect all current and historical staff user_ids for this site so
	// staff-authored audit rows are preserved while Root/platform operator metadata is redacted.
	siteStaffUserIDs := map[int]bool{}
	staffUserByStaffID := map[int]int{}
	staffNameByStaffID := map[int]string{}
	var siteStaffRows []model.AdtySiteStaff
	if err := model.DB.Select("id", "user_id", "username").Where("site_id = ?", site.Id).Find(&siteStaffRows).Error; err == nil {
		for _, row := range siteStaffRows {
			if row.UserId > 0 {
				siteStaffUserIDs[row.UserId] = true
				staffUserByStaffID[row.Id] = row.UserId
			}
			if row.Username != "" {
				staffNameByStaffID[row.Id] = row.Username
			}
		}
	}
	views := make([]staffAuditView, 0, len(items))
	lookupIDs := make([]int, 0, len(items)*3)
	for _, item := range items {
		var target struct {
			Id         int `json:"id"`
			StaffId    int `json:"staff_id"`
			CustomerId int `json:"customer_id"`
		}
		// R1-065: Tolerate non-JSON historical audit payloads instead of 500ing the whole page,
		// and avoid conflating request id / balance_request id with staff_id.
		_ = common.UnmarshalJsonStr(item.NewValue, &target)
		staffID := target.StaffId
		if staffID <= 0 {
			switch item.Action {
			case "staff.create", "staff.update", "staff.password", "staff.delete":
				staffID = target.Id
			}
		}
		// R4-042 / R5-040: Redact only Root/platform operator IDs and internal platform
		// review remarks before returning site audit rows to the site owner.
		if item.OperatorId > 0 && item.OperatorId != site.OwnerUserId && !siteStaffUserIDs[item.OperatorId] {
			item.OperatorId = 0
			item.OldValue = ""
			item.NewValue = ""
			switch {
			case strings.HasPrefix(item.Reason, "staff balance approved"):
				item.Reason = "staff balance approved"
			case strings.HasPrefix(item.Reason, "staff balance rejected"):
				item.Reason = "staff balance rejected"
			default:
				item.Reason = ""
			}
		}
		if item.OperatorId > 0 {
			lookupIDs = append(lookupIDs, item.OperatorId)
		}
		if uid := staffUserByStaffID[staffID]; uid > 0 {
			lookupIDs = append(lookupIDs, uid)
		}
		if target.CustomerId > 0 {
			lookupIDs = append(lookupIDs, target.CustomerId)
		}
		views = append(views, staffAuditView{AdtySiteAudit: item, StaffId: staffID, CustomerId: target.CustomerId})
	}
	usernames := model.LookupUsernamesByIDs(model.DB, lookupIDs)
	for i := range views {
		if views[i].OperatorId > 0 {
			views[i].OperatorUsername = usernames[views[i].OperatorId]
		}
		if views[i].StaffId > 0 {
			uid := staffUserByStaffID[views[i].StaffId]
			views[i].StaffUserId = uid
			if uid > 0 && usernames[uid] != "" {
				views[i].StaffUsername = usernames[uid]
			} else {
				views[i].StaffUsername = staffNameByStaffID[views[i].StaffId]
			}
		}
		if views[i].CustomerId > 0 {
			views[i].CustomerUsername = usernames[views[i].CustomerId]
		}
	}
	page.SetTotal(int(total))
	page.SetItems(views)
	common.ApiSuccess(c, page)
}

var (
	adtyOwnerSiteResolver = adtyOwnerSiteOr403
	adtyOwnerStaffExists  = func(siteID, ownerUserID, staffID int) (bool, error) {
		var count int64
		err := model.DB.Model(&model.AdtySiteStaff{}).
			Where("id = ? AND site_id = ? AND owner_user_id = ? AND status <> ?", staffID, siteID, ownerUserID, "deleted").
			Count(&count).Error
		return count == 1, err
	}
	adtyOwnerStaffProofCheck = func(c *gin.Context, operation service.VerificationOperation) bool {
		return middleware.RequireSecurityProof(c, operation) != nil
	}
	adtyOwnerStaffCreateFn = service.CreateAdtyStaff
	adtyOwnerStaffMutateFn = service.MutateAdtyStaff
)

// AdtyStaffRequestDigest computes the canonical SHA-256 hex digest of a staff mutation payload.
func AdtyStaffRequestDigest(v any) string {
	return service.AdtyStaffRequestDigest(v)
}

// AdtySiteOwnerStaffOperation binds action, target, and the exact staff mutation request digest.
func AdtySiteOwnerStaffOperation(action string, target int, input any) service.VerificationOperation {
	return service.AdtySiteOwnerOperation(action, target, input)
}

func adtyRequireOwnerStaffProof(c *gin.Context, action string, target int, input service.AdtyStaffInput) bool {
	return adtyOwnerStaffProofCheck(c, AdtySiteOwnerStaffOperation(action, target, input))
}

func PostAdtyOwnerStaff(c *gin.Context) {
	site, ok := adtyOwnerSiteResolver(c)
	if !ok {
		return
	}
	var input service.AdtyStaffInput
	if common.DecodeJson(c.Request.Body, &input) != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, service.ErrAdtyStaffInput)
		return
	}
	if !adtyRequireOwnerStaffProof(c, service.AdtySiteOwnerActionStaffCreate, site.Id, input) {
		return
	}
	stripDisabledStaffInputPermissions(&input)
	staff, err := adtyOwnerStaffCreateFn(c.GetInt("id"), site.Id, input)
	if err != nil {
		common.ApiErrorStatus(c, service.AdtyStaffErrorStatus(err), err)
		return
	}
	if staff != nil {
		sanitized := sanitizeAdtyStaffView(*staff)
		staff = &sanitized
	}
	common.ApiSuccess(c, staff)
}

func PutAdtyOwnerStaff(c *gin.Context) {
	mutateAdtyOwnerStaff(c, service.AdtySiteOwnerActionStaffUpdate)
}
func PutAdtyOwnerStaffPassword(c *gin.Context) {
	mutateAdtyOwnerStaff(c, service.AdtySiteOwnerActionStaffPassword)
}
func DeleteAdtyOwnerStaff(c *gin.Context) {
	mutateAdtyOwnerStaff(c, service.AdtySiteOwnerActionStaffDelete)
}

func mutateAdtyOwnerStaff(c *gin.Context, action string) {
	site, ok := adtyOwnerSiteResolver(c)
	if !ok {
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	var input service.AdtyStaffInput
	if common.DecodeJson(c.Request.Body, &input) != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, service.ErrAdtyStaffInput)
		return
	}
	// Reject foreign IDs before consuming the proof.
	exists, err := adtyOwnerStaffExists(site.Id, c.GetInt("id"), id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !exists {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Staff account not found"))
		return
	}
	if !adtyRequireOwnerStaffProof(c, action, id, input) {
		return
	}
	if action == service.AdtySiteOwnerActionStaffUpdate {
		stripDisabledStaffInputPermissions(&input)
	}
	staff, err := adtyOwnerStaffMutateFn(c.GetInt("id"), site.Id, id, action, input)
	if err != nil {
		common.ApiErrorStatus(c, service.AdtyStaffErrorStatus(err), err)
		return
	}
	if staff != nil {
		sanitized := sanitizeAdtyStaffView(*staff)
		staff = &sanitized
	}
	common.ApiSuccess(c, staff)
}

func adtyStaffRequest(c *gin.Context, permission string) (*model.AdtySiteStaff, *model.AdtySite, bool) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtyStaffDenied)
		return nil, nil, false
	}
	staff, site, err := service.AdtyStaffAccess(identity.UserID, identity.UserAuthVersion, permission)
	if err != nil {
		common.ApiErrorStatus(c, service.AdtyStaffErrorStatus(err), err)
		return nil, nil, false
	}
	if hostSite := service.AdtySiteForRequest(c); hostSite != nil && hostSite.Id != site.Id {
		common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtyStaffDenied)
		return nil, nil, false
	}
	return staff, service.AdtyStaffCustomerSite(site, staff), true
}

func GetAdtyStaffSelf(c *gin.Context) {
	staff, site, ok := adtyStaffRequest(c, "")
	if !ok {
		return
	}
	common.ApiSuccess(c, gin.H{"staff": sanitizeAdtyStaffView(service.AdtyStaffViewOf(*staff)), "site_name": service.AdtySiteDisplayHost(site)})
}

func PostAdtyStaffCustomerPasswordReset(c *gin.Context) {
	if _, _, ok := adtyStaffRequest(c, ""); !ok {
		return
	}
	common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtyStaffCustomerPasswordReset)
}

func PostAdtyStaffCustomerDisable(c *gin.Context) {
	manageAdtyStaffCustomer(c, service.AdtyStaffActionCustomerDisable, "customers.disable")
}

func GetAdtyStaffBalanceRequests(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Staff balance requests have been disabled."))
}

func GetAdtyOwnerBalanceRequests(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Staff balance requests have been disabled."))
}

func GetAdtyAdminBalanceRequests(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Staff balance requests have been disabled."))
}

func PostAdtyStaffBalanceRequest(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Staff balance requests have been disabled."))
}

func PostAdtyOwnerBalanceReview(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Staff balance requests have been disabled."))
}

func PostAdtyAdminBalanceReview(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusGone, common.NewMessage("Staff balance requests have been disabled."))
}

func manageAdtyStaffCustomer(c *gin.Context, action, permission string) {
	_, site, ok := adtyStaffRequest(c, permission)
	if !ok {
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	if _, err := service.GetAdtyOwnerCustomer(site, id); err != nil {
		adtyOwnerHiddenOrError(c, err)
		return
	}
	// There are deliberately no mutable customer fields in this contract.
	var body map[string]common.RawMessage
	if common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 1024), &body) != nil || len(body) != 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	if middleware.RequireSecurityProof(c, service.AdtyStaffCustomerOperation(action, id)) == nil {
		return
	}
	identity, _ := middleware.GetSessionAuthIdentity(c)
	if _, err := service.ManageAdtyStaffCustomer(identity.UserID, identity.UserAuthVersion, id, action); err != nil {
		common.ApiErrorStatus(c, service.AdtyStaffErrorStatus(err), err)
		return
	}
	common.ApiSuccess(c, nil)
}

// Record access before sending any customer data; no body, password or keys.
func adtyStaffReadAudit(c *gin.Context, staff *model.AdtySiteStaff, action string, target int) bool {
	if err := service.RecordAdtyStaffReadAudit(c, staff, action, target); err != nil {
		common.ApiError(c, err)
		return false
	}
	return true
}

func GetAdtyStaffCustomers(c *gin.Context) {
	staff, site, ok := adtyStaffRequest(c, "customers.read")
	if !ok {
		return
	}
	page, offset, limit := adtyPage(c)
	items, total, err := service.ListAdtyOwnerCustomers(site, offset, limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !adtyStaffReadAudit(c, staff, "staff.customers.read", 0) {
		return
	}
	page.SetItems(items)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}

func GetAdtyStaffCustomer(c *gin.Context) {
	staff, site, ok := adtyStaffRequest(c, "customers.read")
	if !ok {
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	item, err := service.GetAdtyOwnerCustomer(site, id)
	if err != nil {
		adtyOwnerHiddenOrError(c, err)
		return
	}
	if !adtyStaffReadAudit(c, staff, "staff.customer.read", id) {
		return
	}
	common.ApiSuccess(c, item)
}

func GetAdtyStaffCustomerTopUps(c *gin.Context) {
	staff, site, ok := adtyStaffRequest(c, "customers.topups")
	if !ok {
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	page, offset, limit := adtyPage(c)
	items, total, err := service.ListAdtyOwnerCustomerTopUps(site, id, offset, limit)
	if err != nil {
		adtyOwnerHiddenOrError(c, err)
		return
	}
	if !adtyStaffReadAudit(c, staff, "staff.topups.read", id) {
		return
	}
	page.SetItems(items)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}

// Employee order data uses the unified SQL scope with an explicit minimal
// projection. Permission to read a recharge never grants financial trace data.
func GetAdtyStaffCustomerOrders(c *gin.Context) {
	staff, site, ok := adtyStaffRequest(c, "customers.read")
	if !ok {
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	if _, err := service.GetAdtyOwnerCustomer(site, id); err != nil {
		adtyOwnerHiddenOrError(c, err)
		return
	}
	var customer model.AdtySiteCustomer
	if err := model.DB.Where("user_id = ? AND site_id = ?", id, site.Id).First(&customer).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Customer not found"))
		return
	}
	visibility := service.AdtySiteVisibility(site)
	kinds := []string{}
	if visibility["customer_topups"] == "on" {
		kinds = append(kinds, OrderTypeTopup, OrderTypeVoucher)
	}
	if visibility["customer_usage"] == "on" {
		kinds = append(kinds, OrderTypeConsumption)
	}
	kind, valid := normalizeUnifiedOrderType(c.Query("order_type"))
	allowed := kind == "" && len(kinds) > 0
	for _, grant := range kinds {
		allowed = allowed || kind == grant
	}
	if !valid || !allowed {
		common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtyStaffDenied)
		return
	}
	for _, parameter := range []string{"provider", "method", "channel_id", "team_id", "source_type"} {
		if c.Query(parameter) != "" {
			common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtyStaffDenied)
			return
		}
	}
	query, ok := adtyUnifiedOrdersQuery(c, &site.Id, &id)
	if !ok {
		return
	}
	query = query.Where("o.order_type IN ? AND o.create_time >= ?", kinds, customer.JoinedAt)
	type staffOrder struct {
		TradeNo     string          `json:"trade_no"`
		OrderType   string          `json:"order_type"`
		Status      string          `json:"status"`
		FaceUSD     decimal.Decimal `json:"face_usd"`
		CreatedAt   int64           `json:"created_at"`
		CompletedAt int64           `json:"completed_at"`
	}
	tradeNo := c.Param("trade_no")
	if tradeNo != "" {
		if len(tradeNo) > 255 || (c.Query("section") != "" && c.Query("section") != "business") {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		query = query.Where("o.trade_no = ?", tradeNo)
	}
	page := common.GetPageQuery(c)
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	items := []staffOrder{}
	if err := query.Select("o.trade_no, o.order_type, o.status, o.face_usd, o.create_time AS created_at, o.complete_time AS completed_at").
		Order("o.create_time DESC, o.source_type DESC, o.reference DESC").Limit(page.GetPageSize()).Offset(page.GetStartIdx()).Scan(&items).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if tradeNo != "" && (total != 1 || len(items) != 1) {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Order not found"))
		return
	}
	if !adtyStaffReadAudit(c, staff, "staff.orders.read", id) {
		return
	}
	if tradeNo != "" {
		common.ApiSuccess(c, gin.H{"order": items[0]})
		return
	}
	page.SetTotal(int(total))
	page.SetItems(items)
	common.ApiSuccess(c, page)
}

type adtyStaffUsage struct {
	Id               int    `json:"id"`
	CreatedAt        int64  `json:"created_at"`
	Type             int    `json:"type"`
	ModelName        string `json:"model_name"`
	Quota            int    `json:"quota"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
}

func GetAdtyStaffCustomerLogs(c *gin.Context) {
	staff, site, ok := adtyStaffRequest(c, "customers.usage")
	if !ok {
		return
	}
	id, ok := adtyPathId(c)
	if !ok {
		return
	}
	if service.AdtySiteVisibility(site)["request_logs"] != "on" {
		common.ApiErrorStatus(c, http.StatusForbidden, service.ErrAdtySiteHidden)
		return
	}
	if _, err := service.GetAdtyOwnerCustomer(site, id); err != nil {
		adtyOwnerHiddenOrError(c, err)
		return
	}
	var customer model.AdtySiteCustomer
	if err := model.DB.Where("user_id = ? AND site_id = ?", id, staff.SiteId).First(&customer).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Customer not found"))
		return
	}
	page, offset, limit := adtyPage(c)
	query := model.LOG_DB.Model(&model.Log{}).Where("user_id = ? AND created_at >= ? AND type IN ?", id, customer.JoinedAt, []int{model.LogTypeConsume, model.LogTypeRefund})
	var total int64
	items := []adtyStaffUsage{}
	if err := query.Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := query.Select("id", "created_at", "type", "model_name", "quota", "prompt_tokens", "completion_tokens").Order("id DESC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if !adtyStaffReadAudit(c, staff, "staff.usage.read", id) {
		return
	}
	page.SetItems(items)
	page.SetTotal(int(total))
	common.ApiSuccess(c, page)
}
