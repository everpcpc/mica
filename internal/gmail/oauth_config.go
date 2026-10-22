package gmail

var embeddedClientID, embeddedClientSecret string

func EmbeddedCredentials() (string, string) {
	return embeddedClientID, embeddedClientSecret
}
