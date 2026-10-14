// Package branding contains display defaults generated from the distributor's
// branding.json. Product IDs, data locations and license keys are independent.
package branding

import (
	_ "embed"
	"encoding/json"
)

type Identity struct {
	AppName    string `json:"app_name"`
	BrandShort string `json:"brand_short"`
	BrandLogo  string `json:"brand_logo"`
}

//go:embed defaults.json
var defaultsJSON []byte

var Default = func() Identity {
	var identity Identity
	if err := json.Unmarshal(defaultsJSON, &identity); err != nil {
		panic(err)
	}
	return identity
}()
