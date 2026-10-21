package run

import "strings"

// Redact replaces every occurrence of each non-empty secret with "***".
// An empty secret does not change the text.
func Redact(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		text = strings.ReplaceAll(text, secret, "***")
	}
	return text
}
