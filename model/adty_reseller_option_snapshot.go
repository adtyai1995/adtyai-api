package model

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/setting/adty_setting"
	"gorm.io/gorm"
)

// ResellerWriteMu orders commit and publication of reseller settings within a node.
var ResellerWriteMu sync.Mutex

func LockResellerOptionsTx(tx *gorm.DB) error {
	if tx.Dialector.Name() == "postgres" {
		return tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", adty_setting.ResellerSettingModule).Error
	}
	return nil
}

// ResellerOptionSnapshot reads one database snapshot, including an opaque change
// token for encrypted credentials without exposing their values to the client.
func ResellerOptionSnapshot(tx *gorm.DB) (map[string]string, map[string]string, error) {
	prefix := adty_setting.ResellerSettingModule + "."
	var rows []Option
	if err := tx.Where(tx.Statement.Quote("key")+" LIKE ?", prefix+"%").Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	values, versions := map[string]string{}, map[string]string{}
	for _, row := range rows {
		key, ok := strings.CutPrefix(row.Key, prefix)
		if !ok {
			continue
		}
		value := row.Value
		if isAdtySecretOption(row.Key) && value != "" && !strings.HasPrefix(value, "adty:secret:") {
			// Use the existing, locked one-time credential upgrade before deriving a public revision.
			if _, _, err := readOption(tx, row.Key, true); err != nil {
				return nil, nil, err
			}
			var sealed Option
			if err := tx.Where(&Option{Key: row.Key}).First(&sealed).Error; err != nil {
				return nil, nil, err
			}
			value = sealed.Value
		}
		versions[key] = fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
		if isAdtySecretOption(row.Key) {
			var err error
			value, err = decodeAdtyOptionSecret(row.Key, value)
			if err != nil {
				value = ""
			} // existing credential-reentry warning remains visible
		}
		values[key] = value
	}
	return values, versions, nil
}
