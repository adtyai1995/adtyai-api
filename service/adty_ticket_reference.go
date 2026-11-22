package service

import (
	"strconv"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// A link is an identified record the customer can access. Unverified details
// and reports about missing records can be entered in the ticket description.
func validateTicketReference(tx *gorm.DB, actor adtyTicketActor, teamID int64, kind, ref string) error {
	if kind == "" && ref == "" {
		return nil
	}
	if ref == "" || len(ref) > 64 {
		return ErrAdtyTicketInvalid
	}
	id, _ := strconv.ParseInt(ref, 10, 64)
	var q *gorm.DB
	switch kind {
	case "topup", "refund", "team_order", "team_refund":
		teamOrder := kind == "team_order" || kind == "team_refund"
		if teamOrder != (teamID > 0) {
			return ErrAdtyTicketInvalid
		}
		q = tx.Model(&model.AdtyTopupOrder{}).Where("team_id=?", teamID)
		if teamID == 0 || actor.TeamRole != model.AdtyTeamRoleLeader {
			q = q.Where("user_id=?", actor.User.Id)
		}
		if kind == "refund" || kind == "team_refund" {
			refunds := tx.Model(&model.AdtyManualRefund{}).Select("trade_no").Where("refund_id=?", ref)
			byOrder := tx.Model(&model.AdtyManualRefund{}).Select("trade_no").Where("trade_no=?", ref)
			q = q.Where("(trade_no=? AND (refunded_at > 0 OR trade_no IN (?))) OR trade_no IN (?)", ref, byOrder, refunds)
		} else {
			q = q.Where("trade_no=?", ref)
		}
	case "ledger":
		if teamID != 0 {
			return ErrAdtyTicketInvalid
		}
		q = tx.Model(&model.AdtyBalanceLedger{}).Where("user_id=?", actor.User.Id).Where("id=? OR ref_id=?", id, ref)
	case "voucher":
		if teamID != 0 {
			return ErrAdtyTicketInvalid
		}
		q = tx.Model(&model.AdtyVoucher{}).Where("id=? OR trade_no=?", id, ref).Where("redeemer_id=? OR designated_owner_id=? OR issuer_id=?", actor.User.Id, actor.User.Id, actor.User.Id)
	case "team_balance":
		if teamID == 0 || teamID != id {
			return ErrAdtyTicketInvalid
		}
		return nil // The active team and membership have already been resolved.
	case "team_key":
		if teamID == 0 || id <= 0 {
			return ErrAdtyTicketInvalid
		}
		q = tx.Model(&model.AdtyTeamKey{}).Where("team_id=? AND token_id=?", teamID, id)
		if actor.TeamRole != model.AdtyTeamRoleLeader {
			q = q.Where("user_id=?", actor.User.Id)
		}
	case "request":
		var op model.BillingOperation
		found := tx.Select("operation_key", "user_id", "payer_team_id").Where("operation_key IN ?", []string{"request:" + ref, ref}).Limit(1).Find(&op)
		if found.Error != nil {
			return found.Error
		}
		if found.RowsAffected > 0 {
			if teamID != op.PayerTeamId || (op.UserId != actor.User.Id && (teamID == 0 || actor.TeamRole != model.AdtyTeamRoleLeader)) {
				return ErrAdtyTicketInvalid
			}
			return nil
		}
		if teamID != 0 || model.LOG_DB == nil {
			return ErrAdtyTicketInvalid
		}
		q = model.LOG_DB.Model(&model.Log{}).Where("user_id=? AND request_id=?", actor.User.Id, ref)
	default:
		return ErrAdtyTicketInvalid
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrAdtyTicketInvalid
	}
	return nil
}
