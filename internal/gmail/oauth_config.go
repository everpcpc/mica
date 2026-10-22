package gmail

var embeddedClientID, embeddedClientSecret, embeddedCloudProject string

func EmbeddedCredentials() (string, string) {
	return embeddedClientID, embeddedClientSecret
}

func EmbeddedProjectID() string {
	return embeddedCloudProject
}
