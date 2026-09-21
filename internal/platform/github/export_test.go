package github

import "time"

// SignJWTForTest lets the live tests call endpoints that the client does not
// have, such as a token with fewer permissions than the App.
func SignJWTForTest(cred AppCredentials) (string, error) {
	return signJWT(cred, time.Now())
}
