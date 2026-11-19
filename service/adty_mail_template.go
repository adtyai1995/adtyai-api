package service

import (
	_ "embed"
	"html/template"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/model"
)

//go:embed templates/transactional.gohtml
var adtyMailLayout string

var adtyMailTemplate = template.Must(template.New("transactional").Parse(adtyMailLayout))

// AdtyMailMessage contains plain text. Only the checked-in layout supplies markup.
type AdtyMailMessage struct {
	Title, Intro, Code, ActionLabel, ActionURL, Footnote string
	Paragraphs                                           []string
}

func AdtyRenderMail(site *model.AdtySite, m AdtyMailMessage) string {
	brand := AdtyBrandFor(site)
	logo := ""
	if site != nil {
		name, local := strings.CutPrefix(brand.LogoURL, AdtySiteAssetPrefix)
		if local && strings.HasPrefix(name, "site-"+strconv.Itoa(site.Id)+"-") && adtySiteAssetName.MatchString(name) && !strings.HasSuffix(name, ".svg") {
			logo = AdtySitePublicBaseURL(site) + brand.LogoURL
		}
	}
	if u, err := url.Parse(m.ActionURL); err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || m.ActionLabel == "" {
		m.ActionURL = ""
	}
	var out strings.Builder
	view := struct {
		AdtyMailMessage
		BrandName, LogoURL string
	}{m, brand.Name, logo}
	// Static template, concrete strings, and a strings.Builder: no fallible I/O.
	if err := adtyMailTemplate.Execute(&out, view); err != nil {
		panic(err)
	}
	return out.String()
}
