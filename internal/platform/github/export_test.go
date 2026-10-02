package github

import "time"

// SignJWTForTest lets the live tests call endpoints that the client does not
// have, such as a token with fewer permissions than the App.
func SignJWTForTest(cred AppCredentials) (string, error) {
	// The real GitHub checks the JWT against the real time.
	return signJWT(cred, time.Now())
}
