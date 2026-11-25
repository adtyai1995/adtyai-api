package model

// 大陆自建边缘节点的证书、ACME 账户和运行状态表。
// 私钥统一用 SealAdtyPurposeSecret 加密后入库，所有字段不直接序列化私钥明文。

const (
	AdtyEdgeCertKindWildcard = "wildcard"
	AdtyEdgeCertKindCustom   = "custom"

	AdtyEdgeACMEEnvProduction = "production"
	AdtyEdgeACMEEnvStaging    = "staging"

	AdtyEdgeStateKeyManifestPull = "last_manifest_pull"
	AdtyEdgeStateKeyDNSSync      = "dns_sync"
	AdtyEdgeStateKeyProbe        = "probe"
)

// AdtyEdgeCert 保存边缘节点用的一张 TLS 证书（平台泛域名或站长网站域名）。
type AdtyEdgeCert struct {
	Id           int    `json:"id" gorm:"primaryKey"`
	CertName     string `json:"cert_name" gorm:"uniqueIndex;type:varchar(253);not null"`
	Kind         string `json:"kind" gorm:"type:varchar(16);not null;index"`
	DomainId     int    `json:"domain_id" gorm:"default:0;index"`
	AliasFQDN    string `json:"alias_fqdn" gorm:"type:varchar(253)"`
	Environment  string `json:"environment" gorm:"type:varchar(16);default:'production'"`
	Status       string `json:"status" gorm:"type:varchar(24);not null;index"`
	CertPEM      string `json:"-" gorm:"type:text"`
	KeyEncrypted string `json:"-" gorm:"type:text"`
	NotBefore    int64  `json:"not_before" gorm:"default:0"`
	NotAfter     int64  `json:"not_after" gorm:"default:0;index"`
	LastError    string `json:"last_error" gorm:"type:text"`
	Attempts     int    `json:"attempts" gorm:"default:0"`
	NextRunAt    int64  `json:"next_run_at" gorm:"default:0;index"`
	LeaseToken   string `json:"-" gorm:"type:varchar(64)"`
	LeaseUntil   int64  `json:"-" gorm:"default:0;index"`
	LastIssuedAt int64  `json:"last_issued_at" gorm:"default:0"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt    int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (AdtyEdgeCert) TableName() string { return "adty_edge_certs" }

// AdtyEdgeACMEAccount 保存按环境隔离（production / staging）的 ACME 账户私钥（已加密）和 URI。
type AdtyEdgeACMEAccount struct {
	Id           int    `json:"id" gorm:"primaryKey"`
	Environment  string `json:"environment" gorm:"uniqueIndex;type:varchar(16);not null"`
	AccountURI   string `json:"account_uri" gorm:"type:varchar(512)"`
	KeyEncrypted string `json:"-" gorm:"type:text"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt    int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (AdtyEdgeACMEAccount) TableName() string { return "adty_edge_acme_accounts" }

// AdtyEdgeState 保存边缘节点的轻量运行状态（最近清单拉取、DNS 同步、源站探测结果）。
type AdtyEdgeState struct {
	StateKey  string `json:"state_key" gorm:"primaryKey;type:varchar(64)"`
	Value     string `json:"value" gorm:"type:text"`
	UpdatedAt int64  `json:"updated_at" gorm:"default:0"`
}

func (AdtyEdgeState) TableName() string { return "adty_edge_states" }
