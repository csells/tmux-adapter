package wsbase

import (
	"net/http/httptest"
	"testing"
)

func TestIsAuthorizedRequestWithoutToken(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost:8080/ws", nil)
	if !IsAuthorizedRequest("", req) {
		t.Fatal("expected request without configured token to be authorized")
	}
}

func TestIsAuthorizedRequestBearerToken(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost:8080/ws", nil)
	req.Header.Set("Authorization", "Bearer secret-token")

	if !IsAuthorizedRequest("secret-token", req) {
		t.Fatal("expected bearer token to authorize request")
	}
}

func TestIsAuthorizedRequestQueryToken(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost:8080/ws?token=secret-token", nil)

	if !IsAuthorizedRequest("secret-token", req) {
		t.Fatal("expected query token to authorize request")
	}
}

func TestIsAuthorizedRequestRejectsInvalidToken(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost:8080/ws?token=wrong", nil)
	req.Header.Set("Authorization", "Bearer also-wrong")

	if IsAuthorizedRequest("secret-token", req) {
		t.Fatal("expected invalid tokens to be rejected")
	}
}

func TestIsAuthorizedRequestBearerTokenWithWhitespace(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost:8080/ws", nil)
	req.Header.Set("Authorization", "Bearer   secret-token  ")

	if !IsAuthorizedRequest("  secret-token  ", req) {
		t.Fatal("expected bearer token with whitespace to authorize request")
	}
}

func TestIsAuthorizedRequestNoMatchingQuery(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost:8080/ws", nil)

	if IsAuthorizedRequest("secret-token", req) {
		t.Fatal("expected request with no token to be rejected when server requires one")
	}
}

func TestTokensEqualMatching(t *testing.T) {
	if !TokensEqual("abc", "abc") {
		t.Fatal("expected equal tokens to return true")
	}
}

func TestTokensEqualNotMatching(t *testing.T) {
	if TokensEqual("abc", "xyz") {
		t.Fatal("expected unequal tokens to return false")
	}
}

func TestTokensEqualEmptyExpected(t *testing.T) {
	if TokensEqual("", "abc") {
		t.Fatal("expected empty expected token to return false")
	}
}

func TestTokensEqualEmptyActual(t *testing.T) {
	if TokensEqual("abc", "") {
		t.Fatal("expected empty actual token to return false")
	}
}

func TestTokensEqualBothEmpty(t *testing.T) {
	if TokensEqual("", "") {
		t.Fatal("expected both empty tokens to return false")
	}
}
