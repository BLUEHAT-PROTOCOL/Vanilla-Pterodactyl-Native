package api

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"ptero-native/internal/auth"
)

// signToken signs claims with HS256 (wraps auth.SignJWT).
func signToken(secret string, claims map[string]interface{}) (string, error) {
	return auth.SignJWT(secret, claims)
}

// verifyToken verifies an HS256 token (wraps auth.ParseJWT).
func verifyToken(token, secret string) (*auth.JWTClaims, error) {
	return auth.ParseJWT(token, secret)
}

// unverifiedClaims decodes JWT claims without verification (to locate the server
// whose secret must be used for verification).
func unverifiedClaims(token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errBadToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errBadToken
	}
	var m map[string]interface{}
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil, errBadToken
	}
	return m, nil
}

type simpleErr string

func (e simpleErr) Error() string { return string(e) }

const errBadToken simpleErr = "malformed token"
