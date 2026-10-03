package github

import "time"

// SignJWTForTest lets the live tests call endpoints that the client does not
// have, such as a token with fewer permissions than the App.
func SignJWTForTest(cred AppCredentials) (string, error) {
	// The real GitHub checks the JWT against the real time.
	return signJWT(cred, time.Now())
}

// SetNowForTest replaces the clock of the client.
func (c *AppClient) SetNowForTest(now func() time.Time) {
	c.now = now
}

// RememberInstallationForTest records the installation of a token, as the
// creation of a token does.
func (c *AppClient) RememberInstallationForTest(token string, id int64, expiresAt time.Time) {
	c.rememberInstallation(token, id, expiresAt)
}
