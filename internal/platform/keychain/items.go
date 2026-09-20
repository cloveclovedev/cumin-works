package keychain

// The Keychain items of cumin. docs/ja/designs/cumin-core.md defines them.

// Service is the service name of every Keychain item of cumin.
const Service = "cumin-works"

// PrivateKeyAccount returns the account name of the private key of one GitHub
// App. The value of the item is the PEM in base64 on one line (SetBase64).
//
// The Client ID is in the Host settings file, so the settings alone name the
// item, and the code holds no App name and no organization name.
func PrivateKeyAccount(clientID string) string {
	return "github-app-private-key/" + clientID
}
