package google

import (
	"testing"
)

func TestExtractGoogleValidationURL(t *testing.T) {
	sampleChallenge := `Cloud Code Assist API error (400): {
		"error": {
			"code": 400,
			"message": "Validation required to continue",
			"status": "INVALID_ARGUMENT",
			"details": [
				{
					"reason": "VALIDATION_REQUIRED",
					"metadata": {
						"validation_url": "https://accounts.google.com/challenge/recaptcha?id=123"
					}
				}
			]
		}
	}`

	url := ExtractGoogleValidationURL(sampleChallenge)
	expected := "https://accounts.google.com/challenge/recaptcha?id=123"
	if url != expected {
		t.Fatalf("expected %q, got %q", expected, url)
	}

	msg := FormatGoogleValidationRequiredMessage(url, "dev@example.com")
	if msg != "Account verification required for dev@example.com. Visit https://accounts.google.com/challenge/recaptcha?id=123 to unlock your account, then retry." {
		t.Fatalf("unexpected formatted message: %q", msg)
	}

	// Normal error without challenge
	normalErr := `{"error":{"code":429,"message":"Quota exceeded","status":"RESOURCE_EXHAUSTED"}}`
	if u := ExtractGoogleValidationURL(normalErr); u != "" {
		t.Fatalf("expected empty url for standard 429, got %q", u)
	}

	// Non-JSON error
	if u := ExtractGoogleValidationURL("502 Bad Gateway"); u != "" {
		t.Fatalf("expected empty url for non-JSON, got %q", u)
	}
}
