package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 这条是**守卫**，不是覆盖。
//
// `BillingSession.Settle` 里那段 legacy 结算路径（`funding.Settle`，正数补扣
// 自 ISS-R4-0054 起直接返回 `ErrLegacyWalletCharge`）运行期不可达，
// 唯一原因是生产只有一个构造点、
// 而它总把 `operationKey` 设成非空。**那个不变量没有任何强制。**
//
// 所以这条测试守的是「只有一个构造点」这件事本身：
// **谁新增第二个 `BillingSession{}` 构造点、并且可能不设 operationKey，
// 这里就红。** 红了不代表新代码一定错，代表「先去读 ISS-R4-0046 再决定
// 那段 legacy 分支要不要保留」。
func TestBillingSessionAlwaysCarriesOperationKey(t *testing.T) {
	raw, err := os.ReadFile("billing_session.go")
	require.NoError(t, err)
	source := stripLineComments(string(raw))

	const marker = "&BillingSession{"
	count := strings.Count(source, marker)
	assert.Equal(t, 1, count,
		"BillingSession 的构造点从 1 变成 %d 处：新的构造点若可能不设 operationKey，Settle 的 legacy 分支会静默复活（见 ISS-R4-0046）", count)

	litCount, hasOuterOperationKey := inspectBillingSessionLiterals(t, "billing_session.go", string(raw))
	assert.Equal(t, 1, litCount,
		"AST 中 BillingSession 复合字面量数量从 1 变成 %d 处", litCount)
	assert.True(t, hasOuterOperationKey,
		"构造 BillingSession 时必须在外层 BillingSession 字面量上直接给 operationKey 赋非空值（不能只设内嵌 WalletFunding.operationKey），否则 Settle 会走 legacy 分支")
}

func TestBillingSessionOperationKeyGuardRejectsInnerOnlyOperationKey(t *testing.T) {
	innerOnly := `package service
func newBrokenSession(opKey string) *BillingSession {
	return &BillingSession{
		funding: &WalletFunding{
			operationKey: opKey,
		},
	}
}
`
	litCount, hasOuter := inspectBillingSessionLiterals(t, "synthetic_inner_only.go", innerOnly)
	assert.Equal(t, 1, litCount)
	assert.False(t, hasOuter, "仅在内层 WalletFunding 设置 operationKey 时必须判为不满足外层 BillingSession.operationKey 守卫")

	emptyOuter := `package service
func newEmptyKeySession(opKey string) *BillingSession {
	return &BillingSession{
		funding: &WalletFunding{
			operationKey: opKey,
		},
		operationKey: "",
	}
}
`
	litCount, hasOuter = inspectBillingSessionLiterals(t, "synthetic_empty_outer.go", emptyOuter)
	assert.Equal(t, 1, litCount)
	assert.False(t, hasOuter, "外层 BillingSession.operationKey 为空字面量时必须判为不满足守卫")

	validOuter := `package service
func newValidSession(opKey string) *BillingSession {
	return &BillingSession{
		funding: &WalletFunding{
			operationKey: opKey,
		},
		operationKey: opKey,
	}
}
`
	litCount, hasOuter = inspectBillingSessionLiterals(t, "synthetic_valid_outer.go", validOuter)
	assert.Equal(t, 1, litCount)
	assert.True(t, hasOuter)
}

// 这条钉住「有且只有一处生产构造点」的另一个侧面：包外不能再有
// `BillingSession{`（比如在别的文件里直接拼一个）。
func TestNoOtherBillingSessionLiteralsInService(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, readErr := os.ReadFile(name)
		require.NoError(t, readErr)
		source := stripLineComments(string(raw))
		if name == "billing_session.go" {
			continue
		}
		assert.NotContains(t, source, "&BillingSession{",
			"%s 里新增了 BillingSession 构造点：settle 的 legacy 分支会静默复活（见 ISS-R4-0046）", name)
		litCount, _ := inspectBillingSessionLiterals(t, name, string(raw))
		assert.Zero(t, litCount,
			"%s 里新增了 BillingSession 复合字面量：settle 的 legacy 分支会静默复活（见 ISS-R4-0046）", name)
	}
}

func inspectBillingSessionLiterals(t *testing.T, filename string, source string) (count int, allCarryOuterOperationKey bool) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, source, 0)
	require.NoError(t, err)

	allCarryOuterOperationKey = true
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		ident, ok := lit.Type.(*ast.Ident)
		if !ok || ident.Name != "BillingSession" {
			return true
		}
		count++
		hasOuter := false
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			keyIdent, ok := kv.Key.(*ast.Ident)
			if !ok || keyIdent.Name != "operationKey" {
				continue
			}
			if basic, isBasic := kv.Value.(*ast.BasicLit); isBasic && basic.Kind == token.STRING && strings.Trim(basic.Value, "`\"") == "" {
				continue
			}
			if valIdent, isIdent := kv.Value.(*ast.Ident); isIdent && valIdent.Name == "nil" {
				continue
			}
			hasOuter = true
			break
		}
		if !hasOuter {
			allCarryOuterOperationKey = false
		}
		return true
	})
	if count == 0 {
		allCarryOuterOperationKey = false
	}
	return count, allCarryOuterOperationKey
}

// stripLineComments 丢掉整行注释。只处理 `//` 开头的行——本仓库的注释都是整行
// 行注释，不需要处理行尾注释或块注释。
func stripLineComments(source string) string {
	lines := strings.Split(source, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
