package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetAdtyPaymentScenes(c *gin.Context) {
	config, err := service.GetAdtyPaymentScenes()
	if err != nil {
		writeAdtyTopUpError(c, err)
		return
	}
	common.ApiSuccess(c, config)
}

type adtyPaymentSceneInput struct {
	Key        string                `json:"key"`
	Name       string                `json:"name"`
	Business   string                `json:"business"`
	Enabled    bool                  `json:"enabled"`
	Providers  []string              `json:"providers"`
	Methods    []string              `json:"methods"`
	ChannelIDs []int                 `json:"channel_ids"`
	Currencies []string              `json:"currencies"`
	SiteScope  string                `json:"site_scope"`
	TeamIDs    []int64               `json:"team_ids,omitempty"`
	SiteIDs    []int                 `json:"site_ids"`
	UserTypes  []string              `json:"user_types"`
	MinUSD     common.BoundedDecimal `json:"min_usd"`
	MaxUSD     common.BoundedDecimal `json:"max_usd"`
}

func (in adtyPaymentSceneInput) toModel() model.AdtyPaymentScene {
	return model.AdtyPaymentScene{
		Key:        in.Key,
		Name:       in.Name,
		Business:   in.Business,
		Enabled:    in.Enabled,
		Providers:  in.Providers,
		Methods:    in.Methods,
		ChannelIDs: in.ChannelIDs,
		Currencies: in.Currencies,
		SiteScope:  in.SiteScope,
		TeamIDs:    in.TeamIDs,
		SiteIDs:    in.SiteIDs,
		UserTypes:  in.UserTypes,
		MinUSD:     in.MinUSD.Decimal,
		MaxUSD:     in.MaxUSD.Decimal,
	}
}

func PutAdtyPaymentScenes(c *gin.Context) {
	var input struct {
		ExpectedVersion int64                   `json:"expected_version"`
		Scenes          []adtyPaymentSceneInput `json:"scenes"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
	if common.DecodeJson(c.Request.Body, &input) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	scenes := make([]model.AdtyPaymentScene, 0, len(input.Scenes))
	for _, s := range input.Scenes {
		scenes = append(scenes, s.toModel())
	}
	audit := prepareManageAudit(c, 0, "adty.payment_scenes.update", nil)
	config, err := service.SaveAdtyPaymentScenes(c.GetInt("id"), input.ExpectedVersion, scenes, audit)
	if errors.Is(err, model.ErrAdtyPaymentSceneConflict) {
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("The payment scene configuration has changed. Reload and try again"))
		return
	}
	if err != nil {
		writeAdtyTopUpError(c, err)
		return
	}
	markAuditLogged(c)
	common.ApiSuccess(c, config)
}
