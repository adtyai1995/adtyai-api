package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY0105UserAgreementPG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	withLegalTexts(t,
		map[string]string{"user_agreement": "upstream agreement"},
		map[string]string{"user_agreement": `{"zh-CN":"中文服务条款","en":"English terms"}`},
	)
	code, body := getAdtyLegal(t, "user-agreement", "zh-CN")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "中文服务条款", body)
	code, body = getAdtyLegal(t, "user-agreement", "fr")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "English terms", body)
}

func TestFTRADTY0106PrivacyPolicyPG(t *testing.T) {
	db := adtyTestDB(t)
	pgtest.RecordServerVersion(t, db)
	withLegalTexts(t,
		map[string]string{"privacy_policy": "upstream privacy"},
		map[string]string{"privacy_policy": `{"zh-CN":"中文隐私政策","en":"English privacy"}`},
	)
	code, body := getAdtyLegal(t, "privacy-policy", "zh-CN")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "中文隐私政策", body)
	code, body = getAdtyLegal(t, "privacy-policy", "")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "English privacy", body)
}
