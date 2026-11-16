// Package pgtest 提供「记录 PostgreSQL 证据」的统一入口。
//
// **为什么要一个包**：绑 `t.Fatalf` / `t.Logf` 的 helper 必须收 `testing.TB`，
// 而 `model` 是生产包——生产代码 import "testing" 本身是一条已签的 finding
// （`ISS-R4-0022`）。所以这一层单独放，只有 `_test.go` 会 import 它，
// 它也就不进任何二进制。
package pgtest

import (
	"fmt"
	"testing"

	"gorm.io/gorm"
)

// RequiredPostgresMajor 是方案 v2.7 指定的 PostgreSQL 主版本。
// 补丁号只记录为证据，不作为通过条件；18.x 的任意补丁版本均适用。
const RequiredPostgresMajor = 18

// RecordServerVersion 从实际连接读取版本，检查主版本，并返回 server_version_num。
// PostgreSQL 18.7 的编码为 180007；除以 10000 得到主版本。
func RecordServerVersion(t testing.TB, db *gorm.DB) string {
	t.Helper()
	// 这里自己发这两条 SQL，而不是调 `model.OfficialPostgresServerVersion`：
	// `model` 包内的测试是 `package model`（内部测试），它们 import 本包时，
	// 本包再 import `model` 就是 import cycle。**所以本包不能依赖 model。**
	var versionNum, version string
	if err := db.Raw("SHOW server_version_num").Scan(&versionNum).Error; err != nil {
		t.Fatalf("read server_version_num as evidence: %v", err)
	}
	if err := db.Raw("SHOW server_version").Scan(&version).Error; err != nil {
		t.Fatalf("read server_version as evidence: %v", err)
	}
	if versionNum == "" {
		t.Fatal("live server_version_num must be recorded as evidence")
	}
	var num int
	if _, scanErr := fmt.Sscanf(versionNum, "%d", &num); scanErr != nil {
		t.Fatalf("parse server_version_num %q: %v", versionNum, scanErr)
	}
	if major := num / 10000; major != RequiredPostgresMajor {
		t.Fatalf("this suite targets PostgreSQL %d (its expectations are written for %d.x); live server is %s (%s)",
			RequiredPostgresMajor, RequiredPostgresMajor, versionNum, version)
	}
	t.Logf("evidence only (not a pass/fail condition): server_version_num=%s server_version=%s major=%d",
		versionNum, version, num/10000)
	return versionNum
}
