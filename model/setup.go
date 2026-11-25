package model

import "github.com/QuantumNous/new-api/common"

type Setup struct {
	ID            uint   `json:"id" gorm:"primaryKey"`
	Version       string `json:"version" gorm:"type:varchar(50);not null"`
	InitializedAt int64  `json:"initialized_at" gorm:"type:bigint;not null"`
}

func GetSetup() *Setup {
	var setup Setup
	err := DB.First(&setup).Error
	if err != nil {
		return nil
	}
	return &setup
}

// IsSetupComplete reads persisted installation facts without changing runtime
// configuration. It is used once when preparing the public homepage handler.
// Any missing fact or database error keeps the frontend setup-check fallback.
func IsSetupComplete() bool {
	if DB == nil {
		return false
	}
	var roots, setups, options int64
	if err := DB.Model(&User{}).Where("role = ?", common.RoleRootUser).Count(&roots).Error; err != nil || roots == 0 {
		return false
	}
	if err := DB.Model(&Setup{}).Count(&setups).Error; err != nil || setups == 0 {
		return false
	}
	if err := DB.Model(&Option{}).Where(map[string]any{"key": []string{"SelfUseModeEnabled", "DemoSiteEnabled"}}).Count(&options).Error; err != nil {
		return false
	}
	return options == 2
}
