package google

import (
	"encoding/json"
	"fmt"
	"strings"
)

type googleErrorDetail struct {
	Reason   string            `json:"reason"`
	Metadata map[string]string `json:"metadata"`
}

type googleErrorEnvelope struct {
	Error struct {
		Code    int                 `json:"code"`
		Message string              `json:"message"`
		Status  string              `json:"status"`
		Details []googleErrorDetail `json:"details"`
	} `json:"error"`
}

// ExtractGoogleValidationURL inspects an error response body from Google Cloud Code Assist.
// If the account requires interactive verification (VALIDATION_REQUIRED challenge),
// it extracts and returns the verification URL.
func ExtractGoogleValidationURL(errorBody string) string {
	if !strings.Contains(errorBody, "VALIDATION_REQUIRED") {
		return ""
	}
	start := strings.IndexByte(errorBody, '{')
	if start == -1 {
		return ""
	}
	var env googleErrorEnvelope
	if err := json.Unmarshal([]byte(errorBody[start:]), &env); err != nil {
		return ""
	}
	for _, d := range env.Error.Details {
		if d.Reason == "VALIDATION_REQUIRED" && d.Metadata != nil {
			if u, ok := d.Metadata["validation_url"]; ok && u != "" {
				return u
			}
		}
	}
	return ""
}

// FormatGoogleValidationRequiredMessage builds an actionable error message directing the user or admin
// to resolve the account verification challenge.
func FormatGoogleValidationRequiredMessage(validationURL, email string) string {
	account := ""
	if email != "" {
		account = fmt.Sprintf(" for %s", email)
	}
	return fmt.Sprintf("Account verification required%s. Visit %s to unlock your account, then retry.", account, validationURL)
}
