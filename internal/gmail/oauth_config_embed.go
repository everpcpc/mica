package gmail

import (
	_ "embed"
	"encoding/json"
)

//go:embed oauth-client.json
var oauthClientJSON []byte

func init() {
	var config struct {
		Installed struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		} `json:"installed"`
	}
	if err := json.Unmarshal(oauthClientJSON, &config); err != nil {
		panic("gmail: invalid embedded OAuth client configuration")
	}
	if config.Installed.ClientID == "" || config.Installed.ClientSecret == "" {
		panic("gmail: incomplete embedded OAuth client configuration")
	}
	embeddedClientID = config.Installed.ClientID
	embeddedClientSecret = config.Installed.ClientSecret
}
