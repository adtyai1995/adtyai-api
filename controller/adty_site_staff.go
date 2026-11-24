package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

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
		items = append(items, service.AdtyStaffViewOf(member))
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
		StaffId    int `json:"staff_id"`
		CustomerId int `json:"customer_id"`
	}
	// R5-040: Collect all current and historical staff user_ids for this site so
	// staff-authored audit rows are preserved while Root/platform operator metadata is redacted.
	siteStaffUserIDs := map[int]bool{}
	var staffUserIDs []int
	if err := model.DB.Model(&model.AdtySiteStaff{}).Where("site_id = ?", site.Id).Pluck("user_id", &staffUserIDs).Error; err == nil {
		for _, uid := range staffUserIDs {
			if uid > 0 {
				siteStaffUserIDs[uid] = true
			}
		}
	}
	views := make([]staffAuditView, 0, len(items))
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
		views = append(views, staffAuditView{AdtySiteAudit: item, StaffId: staffID, CustomerId: target.CustomerId})
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
	staff, err := adtyOwnerStaffCreateFn(c.GetInt("id"), site.Id, input)
	if err != nil {
		common.ApiErrorStatus(c, service.AdtyStaffErrorStatus(err), err)
		return
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
	staff, err := adtyOwnerStaffMutateFn(c.GetInt("id"), site.Id, id, action, input)
	if err != nil {
		common.ApiErrorStatus(c, service.AdtyStaffErrorStatus(err), err)
		return
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
	common.ApiSuccess(c, gin.H{"staff": service.AdtyStaffViewOf(*staff), "site_name": service.AdtySiteDisplayHost(site)})
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

func redactRootBalanceRequest(row *model.AdtyStaffBalanceRequest) {
	if row != nil && row.ReviewerRole == "root" {
		row.ReviewerId = 0
		row.ReviewNote = ""
	}
}

func redactRootBalanceRequests(rows []model.AdtyStaffBalanceRequest) {
	for i := range rows {
		redactRootBalanceRequest(&rows[i])
	}
}

func GetAdtyStaffBalanceRequests(c *gin.Context) {
	staff, site, ok := adtyStaffRequest(c, "")
	if !ok {
		return
	}
	page := common.GetPageQuery(c)
	rows, total, err := service.PageStaffOwnBalanceRequests(site.Id, staff.UserId, c.Query("status"), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	redactRootBalanceRequests(rows)
	common.ApiSuccess(c, gin.H{"items": rows, "total": total, "page": page.GetPage(), "page_size": page.GetPageSize()})
}

func GetAdtyOwnerBalanceRequests(c *gin.Context) {
	site, ok := adtyOwnerSiteOr403(c)
	if !ok {
		return
	}
	listOwnerBalanceRequestPage(c, site.Id, true)
}

func GetAdtyAdminBalanceRequests(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	siteID, err := strconv.Atoi(c.Query("site_id"))
	if err != nil || siteID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	listOwnerBalanceRequestPage(c, siteID, false)
}

func listOwnerBalanceRequestPage(c *gin.Context, siteID int, redactRoot bool) {
	page := common.GetPageQuery(c)
	rows, total, err := service.PageStaffBalanceRequests(siteID, c.Query("status"), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if redactRoot {
		redactRootBalanceRequests(rows)
	}
	common.ApiSuccess(c, gin.H{"items": rows, "total": total, "page": page.GetPage(), "page_size": page.GetPageSize()})
}

func PostAdtyStaffBalanceRequest(c *gin.Context) {
	if _, _, ok := adtyStaffRequest(c, service.AdtyStaffBalanceRequest); !ok {
		return
	}
	var body struct {
		CustomerID     int    `json:"customer_id"`
		Direction      string `json:"direction"`
		AmountUSD      string `json:"amount_usd"`
		IdempotencyKey string `json:"idempotency_key"`
		model.AdtyStaffBalanceProvenance
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	amount, err := decimal.NewFromString(strings.TrimSpace(body.AmountUSD))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	key := strings.TrimSpace(body.IdempotencyKey)
	if key == "" {
		key = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	}
	row, err := service.SubmitAdtyStaffBalanceRequest(c.GetInt("id"), body.CustomerID, body.Direction, amount, key, body.AdtyStaffBalanceProvenance)
	if err != nil {
		if errors.Is(err, model.ErrAdtyIdempotencyConflict) {
			common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("This request was already used for another operation"))
			return
		}
		common.ApiError(c, err)
		return
	}
	redactRootBalanceRequest(row)
	common.ApiSuccess(c, row)
}

func PostAdtyOwnerBalanceReview(c *gin.Context) {
	reviewAdtyStaffBalance(c, "owner")
}

func PostAdtyAdminBalanceReview(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	reviewAdtyStaffBalance(c, "root")
}

func reviewAdtyStaffBalance(c *gin.Context, role string) {
	id, ok := adtyInt64Param(c, "id")
	if !ok {
		return
	}
	var body struct {
		Approve bool   `json:"approve"`
		Reason  string `json:"reason"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	// R1-018: Owner balance-request review requires step-up proof bound to (requestID, approve, reason).
	if role == "owner" {
		if _, ok := adtyOwnerSiteOr403(c); !ok {
			return
		}
		if !adtyRequireOwnerProof(c, service.AdtySiteOwnerActionStaffBalanceReview, int(id), service.AdtyOwnerBalanceReviewDigestInput{
			Approve: body.Approve,
			Reason:  body.Reason,
		}) {
			return
		}
	}
	if err := service.ReviewAdtyStaffBalanceRequest(c.GetInt("id"), role, id, body.Approve, body.Reason); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"reviewed": true})
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
	var body map[string]json.RawMessage
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
	after := gin.H{"staff_id": staff.Id, "customer_id": target}
	if err := model.DB.Create(&model.AdtySiteAudit{SiteId: staff.SiteId, OperatorId: staff.UserId, Action: action,
		NewValue: common.GetJsonString(after)}).Error; err != nil {
		common.ApiError(c, err)
		return false
	}
	if err := model.WriteOfficialAuditLog(c, model.OfficialAuditWrite{
		UserId: staff.UserId, Username: c.GetString("username"), ActorRole: c.GetInt("role"),
		SiteID: model.OfficialAuditSiteIDPointer(staff.SiteId), ObjectType: "site.staff",
		ObjectID: strconv.Itoa(staff.Id), Action: "adty." + action, Category: model.AuditCategoryOperation,
		Content: action, After: after, Result: model.AuditResultSuccess, Success: true,
	}); err != nil {
		common.SysLog(common.LogText("official staff audit write failed: %s", err.Error()))
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
