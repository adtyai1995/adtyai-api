package service

import (
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

var ErrAdtySiteOwnerImmutable = common.NewMessage("Site owners cannot be replaced")

type AdtySiteOwnerHandoverInput struct {
	SiteId                   int    `json:"-"`
	OperatorId               int    `json:"-"`
	ExpectedOwnerUserId      int    `json:"expected_owner_user_id"`
	ExpectedOwnershipVersion *int64 `json:"expected_ownership_version"`
	NewOwnerUserId           int    `json:"new_owner_user_id"`
	Reason                   string `json:"reason"`
}

// HandoverAdtySiteOwner refuses owner replacement. Historical ownership rows
// remain readable; this entry must not create a new one or move authority.
func HandoverAdtySiteOwner(input AdtySiteOwnerHandoverInput) (*model.AdtySite, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if input.SiteId <= 0 || input.OperatorId <= 0 || input.ExpectedOwnerUserId <= 0 || input.NewOwnerUserId <= 0 || input.ExpectedOwnerUserId == input.NewOwnerUserId ||
		input.ExpectedOwnershipVersion == nil || *input.ExpectedOwnershipVersion < 0 || input.Reason == "" || utf8.RuneCountInString(input.Reason) > 500 {
		return nil, common.NewMessage("Invalid parameters")
	}
	return nil, ErrAdtySiteOwnerImmutable
}
