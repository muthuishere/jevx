package core

import (
	"strings"

	tn "github.com/muthuishere/toolnexus/golang"
)

// A profile's type is the toolnexus classifier backend that answers it (toolnexus ClassifierBackends). Questions,
// verdicts, the cache and batches are the same for every type; toolnexus builds the request and reads the reply.
const (
	TypeTypeSafe   = string(tn.BackendTypeSafe)   // TypeSafe's System One API; also any System One server (openjevx) at its own url
	TypeOpenRouter = string(tn.BackendOpenRouter) // OpenRouter's System One route
	TypeCloudflare = string(tn.BackendCloudflare) // Cloudflare Workers AI /ai/run: the System One body in a {model, input} envelope
	TypeVercel     = string(tn.BackendVercel)     // Vercel AI Gateway's TypeSafe-compatible System One route
	TypeCustom     = string(tn.BackendCustom)     // a self-hosted System One endpoint; it takes images only when the profile says so
)

// TypeInfo describes a profile type for `jevx profile types`, with the toolnexus preset behind it.
type TypeInfo struct {
	Name    string
	Summary string
	URL     string // the endpoint a profile of this type uses when it gives none
	Model   string // the model it uses when it gives none
	KeyEnv  string // the variable its Authorization header reads when the profile sets no header
}

var typeSummaries = []struct{ name, summary string }{
	{TypeTypeSafe, "TypeSafe's System One API (hosted Jev); with its own url, any System One server such as openjevx"},
	{TypeOpenRouter, "OpenRouter's System One route (typesafe/jev-*)"},
	{TypeCloudflare, "Cloudflare Workers AI /ai/run: the System One body in a {model, input} envelope"},
	{TypeVercel, "Vercel AI Gateway's TypeSafe-compatible System One route"},
	{TypeCustom, "a self-hosted System One endpoint (url required; images only with --images)"},
}

// Types lists every profile type with its toolnexus preset.
var Types = func() []TypeInfo {
	var out []TypeInfo
	for _, s := range typeSummaries {
		pr := tn.ClassifierBackends[tn.ClassifierBackend(s.name)]
		u := pr.BaseURL
		if u != "" && pr.Wire != tn.ClassifierWireCloudflare {
			u += "/systemone"
		}
		out = append(out, TypeInfo{Name: s.name, Summary: s.summary, URL: u, Model: pr.Model, KeyEnv: pr.APIKeyEnv})
	}
	return out
}()

// TypeNames is "typesafe, openrouter, cloudflare, vercel, custom".
func TypeNames() string {
	var n []string
	for _, t := range Types {
		n = append(n, t.Name)
	}
	return strings.Join(n, ", ")
}

// LookupType returns the type's description, or false for an unknown name.
func LookupType(name string) (TypeInfo, bool) {
	for _, t := range Types {
		if t.Name == name {
			return t, true
		}
	}
	return TypeInfo{}, false
}

// Kind is the profile's type: "type", else the legacy "style", else read from a known host, else typesafe.
func (p Profile) Kind() string {
	switch {
	case p.Type != "":
		return p.Type
	case p.Style != "":
		return p.Style
	case strings.Contains(p.URL, "/ai/run"):
		return TypeCloudflare
	case strings.Contains(p.URL, "openrouter.ai"):
		return TypeOpenRouter
	case strings.Contains(p.URL, "ai-gateway.vercel.sh"):
		return TypeVercel
	}
	return TypeTypeSafe
}

// TakesImages says whether images may be sent to this profile: its "images" setting, else toolnexus's support
// table for its type and model (a custom endpoint: no).
func (p Profile) TakesImages() bool {
	if p.Images != nil {
		return *p.Images
	}
	pr, ok := tn.ClassifierBackends[tn.ClassifierBackend(p.Kind())]
	if !ok || pr.Supports == nil {
		return false
	}
	return pr.Supports(p.Model).Images
}

// Preset fills what a new profile of type t leaves out: the model, and with no url the type's own endpoint plus an
// Authorization header reading the type's key variable. A profile with its own url gets no key it did not ask for.
func Preset(t string, p Profile) Profile {
	info, _ := LookupType(t)
	if p.Model == "" {
		p.Model = info.Model
	}
	if p.URL != "" || info.URL == "" {
		return p
	}
	p.URL = info.URL
	if _, has := p.Headers["Authorization"]; !has && info.KeyEnv != "" {
		if p.Headers == nil {
			p.Headers = map[string]string{}
		}
		p.Headers["Authorization"] = "Bearer $" + info.KeyEnv
	}
	return p
}
