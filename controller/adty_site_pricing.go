package controller

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// 站长定价接口（任务 11.4）。站长中心的其余接口在 11.6；要求重新验证身份的二次确认也在 11.6 接上。

// adtyOwnedSite 取当前用户是站长的、没关的站；不是站长返回 403。
func adtyOwnedSite(c *gin.Context) (*model.AdtySite, bool) {
	var sites []model.AdtySite
	if err := model.DB.Where("owner_user_id = ? AND status IN ?", c.GetInt("id"),
		[]string{model.AdtySiteStatusActive, model.AdtySiteStatusPaused, model.AdtySiteStatusClosing}).Limit(1).Find(&sites).Error; err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	if len(sites) == 0 {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("You do not have an open site"))
		return nil, false
	}
	return &sites[0], true
}

type adtySitePricingBody struct {
	DefaultMarkup   *string           `json:"default_markup"`
	ClearDefault    bool              `json:"clear_default"`
	GroupMarkups    map[string]string `json:"group_markups"` // 值为 "0" 表示删掉这个分组的单独价
	ModelMarkups    map[string]string `json:"model_markups"` // 值为 "0" 表示删掉这个模型的独立加价
	ExpectedVersion int64             `json:"expected_version"`
}

// Limit the plain-decimal representation before parsing externally supplied exponents or coefficients.
var retailMarkupPattern = regexp.MustCompile(`^(?:[0-9]{1,2}(?:\.[0-9]{0,4})?|\.[0-9]{1,4})$`)

func parseRetailMarkup(raw string) (decimal.Decimal, error) {
	raw = strings.TrimSpace(raw)
	if !retailMarkupPattern.MatchString(raw) {
		return decimal.Zero, service.ErrAdtySiteSingleMultiplierOnly
	}
	value, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, service.ErrAdtySiteSingleMultiplierOnly
	}
	if value.LessThan(decimal.NewFromInt(1)) || value.GreaterThan(decimal.NewFromInt(10)) {
		return decimal.Zero, service.ErrAdtySiteSingleMultiplierOnly
	}
	return value, nil
}

func saveAdtySitePricing(c *gin.Context, site *model.AdtySite) {
	var body adtySitePricingBody
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if len(body.GroupMarkups) > 0 || len(body.ModelMarkups) > 0 {
		common.ApiError(c, service.ErrAdtySiteSingleMultiplierOnly)
		return
	}
	input := service.AdtySitePricingInput{
		SiteId:          site.Id,
		UserId:          0,
		ClearDefault:    body.ClearDefault,
		ActorId:         c.GetInt("id"),
		ExpectedVersion: body.ExpectedVersion,
	}
	if body.DefaultMarkup != nil && strings.TrimSpace(*body.DefaultMarkup) != "" {
		value, err := parseRetailMarkup(*body.DefaultMarkup)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		input.DefaultMarkup = &value
	}
	result, err := service.SaveAdtySitePricing(input)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

// PutAdtySitePricing 保存全站定价：PUT /api/adty/site/pricing {default_markup, expected_version}。
// 只接受单一全站零售倍率（1x–10x）；不接受分组/模型独立覆盖，供货倍率和主站标准价格不能从这条路径改（SPRC-014）。
func PutAdtySitePricing(c *gin.Context) {
	site, ok := adtyOwnedSite(c)
	if !ok {
		return
	}
	saveAdtySitePricing(c, site)
}

// PutAdtySiteCustomerPricing 已退役（R-32）：分站定价仅支持单一全站倍率（1x–10x），不再支持按客户单独加价。
func PutAdtySiteCustomerPricing(c *gin.Context) {
	if _, ok := adtyOwnedSite(c); !ok {
		return
	}
	common.ApiError(c, service.ErrAdtySiteSingleMultiplierOnly)
}
