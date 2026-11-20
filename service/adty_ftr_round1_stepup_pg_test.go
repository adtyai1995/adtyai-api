package service

import (
	"testing"

	"github.com/QuantumNous/new-api/internal/pgtest"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestFTRADTY2102EmailStepUpPG(t *testing.T) {
	user, identity := setupSecurityStepUpTest(t)
	pgtest.RecordServerVersion(t, model.DB)
	site := &model.AdtySite{OwnerUserId: user.Id, Subdomain: "ftr2102", Status: model.AdtySiteStatusActive}
	require.NoError(t, model.DB.Create(site).Error)

	sent := captureStepUpEmail(t)
	require.NoError(t, IssueSecurityEmailCode(identity, VerificationScopePasswordChange, site, "en"))
	code := extractStepUpCode(t, sent.content)
	require.NotEmpty(t, code)

	proof, err := VerifySecurityInput(identity, VerificationInput{
		Method: VerificationMethodEmailCode,
		Scope:  VerificationScopePasswordChange,
		Code:   code,
		SiteID: site.Id,
	})
	require.NoError(t, err)
	require.NotEmpty(t, proof.ProofToken)

	_, err = VerifySecurityInput(identity, VerificationInput{
		Method: VerificationMethodEmailCode,
		Scope:  VerificationScopePasswordChange,
		Code:   code,
		SiteID: site.Id,
	})
	require.ErrorIs(t, err, ErrVerificationFailed)

	_, err = ConsumeOperationProof(proof.ProofToken, identity, VerificationOperation{Scope: VerificationScopePasswordChange})
	require.NoError(t, err)
}
