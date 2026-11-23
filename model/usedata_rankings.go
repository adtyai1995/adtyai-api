package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type RankingQuotaTotal struct {
	ModelName   string `json:"model_name"`
	TotalTokens int64  `json:"total_tokens"`
}

type RankingQuotaBucket struct {
	ModelName string `json:"model_name"`
	Bucket    int64  `json:"bucket"`
	Tokens    int64  `json:"tokens"`
}

func GetRankingQuotaTotals(startTime int64, endTime int64) ([]RankingQuotaTotal, error) {
	return GetRankingQuotaTotalsForSite(startTime, endTime, 0)
}

func GetRankingQuotaTotalsForSite(startTime int64, endTime int64, siteID int) ([]RankingQuotaTotal, error) {
	var rows []RankingQuotaTotal
	query := DB.Table("quota_data").
		Select("quota_data.model_name AS model_name, sum(quota_data.token_used) as total_tokens").
		Where("quota_data.model_name <> ''").
		Group("quota_data.model_name").
		Having("sum(quota_data.token_used) > 0").
		Order("total_tokens DESC")
	if siteID > 0 {
		query = query.Joins("JOIN users ON users.id = quota_data.user_id").Where("users.site_id = ?", siteID)
	}
	query = applyRankingQuotaTimeRange(query, startTime, endTime)
	err := query.Find(&rows).Error
	return rows, err
}

func GetRankingQuotaBuckets(startTime int64, endTime int64, bucketSize int64) ([]RankingQuotaBucket, error) {
	return GetRankingQuotaBucketsForSite(startTime, endTime, bucketSize, 0)
}

func GetRankingQuotaBucketsForSite(startTime int64, endTime int64, bucketSize int64, siteID int) ([]RankingQuotaBucket, error) {
	if bucketSize <= 0 {
		bucketSize = 3600
	}
	bucketExpr := rankingBucketExpr(bucketSize)
	var rows []RankingQuotaBucket
	query := DB.Table("quota_data").
		Select(fmt.Sprintf("quota_data.model_name AS model_name, %s as bucket, sum(quota_data.token_used) as tokens", bucketExpr)).
		Where("quota_data.model_name <> ''").
		Group(fmt.Sprintf("quota_data.model_name, %s", bucketExpr)).
		Having("sum(quota_data.token_used) > 0").
		Order("bucket ASC")
	if siteID > 0 {
		query = query.Joins("JOIN users ON users.id = quota_data.user_id").Where("users.site_id = ?", siteID)
	}
	query = applyRankingQuotaTimeRange(query, startTime, endTime)
	err := query.Find(&rows).Error
	return rows, err
}

func rankingBucketExpr(bucketSize int64) string {
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		return fmt.Sprintf("FLOOR(quota_data.created_at / %d) * %d", bucketSize, bucketSize)
	}
	return fmt.Sprintf("(quota_data.created_at / %d) * %d", bucketSize, bucketSize)
}

func applyRankingQuotaTimeRange(query *gorm.DB, startTime int64, endTime int64) *gorm.DB {
	if startTime > 0 {
		query = query.Where("quota_data.created_at >= ?", startTime)
	}
	if endTime > 0 {
		query = query.Where("quota_data.created_at <= ?", endTime)
	}
	return query
}
