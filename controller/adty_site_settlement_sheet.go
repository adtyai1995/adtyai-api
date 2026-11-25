package controller

import (
	"bytes"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetAdtyBusinessTimezone(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	view, err := service.LoadAdtyBusinessTimezone()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func PutAdtyBusinessTimezone(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	var body struct {
		Timezone string `json:"timezone"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := service.SaveAdtyBusinessTimezone(body.Timezone); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"saved": true})
}

func GetAdtyOwnerSettlementSheets(c *gin.Context) {
	site, ok := adtyOwnerAccountingSite(c)
	if !ok {
		return
	}
	if err := service.ReviewAdtyPooledSiteFinance(model.DB, site.Id); err != nil {
		common.ApiError(c, err)
		return
	}
	serveSettlementSheets(c, site.Id, false)
}

func GetAdtyAdminSettlementSheets(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("No permission to perform this action"))
		return
	}
	siteID := 0
	if raw := c.Query("site_id"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		siteID = id
	}
	serveSettlementSheets(c, siteID, true)
}

func serveSettlementSheets(c *gin.Context, siteID int, isAdmin bool) {
	if raw, ok := c.GetQuery("sheet_id"); ok {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		row, err := service.GetSettlementSheet(siteID, id)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if !isAdmin {
			sanitized, err := sanitizeOwnerSettlementSheet(row)
			if err != nil {
				common.ApiError(c, err)
				return
			}
			common.ApiSuccess(c, sanitized)
			return
		}
		common.ApiSuccess(c, row)
		return
	}
	if c.Query("p") != "" || c.Query("business_day") != "" || c.Query("page_size") != "" {
		page := common.GetPageQuery(c)
		if err := service.PageSettlementSheets(siteID, c.Query("business_day"), page); err != nil {
			common.ApiError(c, err)
			return
		}
		common.ApiSuccess(c, page)
		return
	}
	// Earlier API clients expect an array when pagination is omitted.
	if siteID > 0 {
		rows, err := service.ListAdtySiteSettlementSheets(siteID)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if !isAdmin {
			sanitized := make([]map[string]common.RawMessage, 0, len(rows))
			for _, row := range rows {
				item, err := sanitizeOwnerSettlementSheet(row)
				if err != nil {
					common.ApiError(c, err)
					return
				}
				sanitized = append(sanitized, item)
			}
			common.ApiSuccess(c, sanitized)
			return
		}
		common.ApiSuccess(c, rows)
		return
	}
	rows, err := service.ListAllAdtySiteSettlementSheets()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

func sanitizeOwnerSettlementSheet(sheet model.AdtySiteSettlementSheet) (map[string]common.RawMessage, error) {
	raw, err := common.Marshal(sheet)
	if err != nil {
		return nil, err
	}
	return sanitizeOwnerSettlementJSONObject(raw)
}

func sanitizeOwnerSettlementJSONObject(raw common.RawMessage) (map[string]common.RawMessage, error) {
	var fields map[string]common.RawMessage
	if err := common.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	delete(fields, "batch_id")
	delete(fields, "event_watermark")
	delete(fields, "sources")
	if legacyRaw, ok := fields["legacy_coverage"]; ok && bytes.HasPrefix(bytes.TrimSpace(legacyRaw), []byte("[")) {
		var entries []map[string]common.RawMessage
		if err := common.Unmarshal(legacyRaw, &entries); err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry == nil {
				continue
			}
			delete(entry, "event_watermark")
		}
		cleaned, err := common.Marshal(entries)
		if err != nil {
			return nil, err
		}
		fields["legacy_coverage"] = cleaned
	}
	if revsRaw, ok := fields["revisions"]; ok && bytes.HasPrefix(bytes.TrimSpace(revsRaw), []byte("[")) {
		var revs []map[string]common.RawMessage
		if err := common.Unmarshal(revsRaw, &revs); err != nil {
			return nil, err
		}
		for _, rev := range revs {
			if rev == nil {
				continue
			}
			delete(rev, "sources")
			delete(rev, "batch_id")
			delete(rev, "event_watermark")
			for _, snapKey := range []string{"before", "after"} {
				snapRaw, exists := rev[snapKey]
				if !exists || !bytes.HasPrefix(bytes.TrimSpace(snapRaw), []byte("{")) {
					continue
				}
				cleanedSnap, err := sanitizeOwnerSettlementJSONObject(snapRaw)
				if err != nil {
					return nil, err
				}
				encodedSnap, err := common.Marshal(cleanedSnap)
				if err != nil {
					return nil, err
				}
				rev[snapKey] = encodedSnap
			}
		}
		cleaned, err := common.Marshal(revs)
		if err != nil {
			return nil, err
		}
		fields["revisions"] = cleaned
	}
	return fields, nil
}
