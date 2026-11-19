package adty_setting

import "github.com/QuantumNous/new-api/setting/config"

// SEOSetting 是两个正式首页的标题、描述和分享图，键名 adty_seo_setting.<json 标签>。
// 正式页只有英文 / 和简体中文 /zh-cn。HomeTitle、HomeDescription 的键是 en 与 zh-CN。
// 某种语言没填就用 en 的；en 也没填时，标题用站名，描述不写。旧语言别名不作为额外首页宣传。
type SEOSetting struct {
	HomeTitle       map[string]string `json:"home_title"`
	HomeDescription map[string]string `json:"home_description"`
	OGImage         string            `json:"og_image"` // 以 / 开头时拼在服务器地址后面
}

var seoSetting = SEOSetting{
	HomeTitle:       map[string]string{},
	HomeDescription: map[string]string{},
	OGImage:         "/adty-og.png",
}

func init() {
	config.GlobalConfig.Register("adty_seo_setting", &seoSetting)
}

// GetSEOSetting 返回当前的首页 SEO 配置。
func GetSEOSetting() *SEOSetting {
	return &seoSetting
}
