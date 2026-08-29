// Package auth implements wings-compatible bearer authentication and JWT helpers.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// TokenSource provides the id->key map for validating panel requests.
type TokenSource interface {
	APIKeys() map[string]string
}

// CheckBearer validates an "Authorization: Bearer <id>.<key>" header.
func CheckBearer(r *http.Request, keys map[string]string) bool {
	h := r.Header.Get("Authorization")
	if h == "" || !strings.HasPrefix(h, "Bearer ") {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	id, key, ok := strings.Cut(token, ".")
	if !ok || id == "" || key == "" {
		return false
	}
	expected, ok := keys[id]
	if !ok {
		// avoid timing side-channel: compare against a random dummy anyway
		subtle.ConstantTimeCompare([]byte(key), []byte("xxxxxxxxxxxxxxxxxxxxxxxx"))
		return false
	}
	return subtle.ConstantTimeCompare([]byte(key), []byte(expected)) == 1
}

// JWTClaims is a permissive JWT claim set.
type JWTClaims struct {
	Sub       string                 `json:"sub"`
	UniqueID  string                 `json:"unique_id"`
	ServerUUID string                `json:"server_uuid,omitempty"`
	Owner     string                 `json:"owner,omitempty"`
	Servers   map[string]interface{} `json:"servers,omitempty"`
	Exp       int64                  `json:"exp"`
	Nbf       int64                  `json:"nbf,omitempty"`
	IssuedAt  int64                  `json:"iat,omitempty"`
	SessionID string                 `json:"jti,omitempty"`
	Extra     map[string]interface{} `json:"-"`
}

// ParseJWT validates an HS256 JWT signed with secret and returns its claims.
func ParseJWT(token, secret string) (*JWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("jwt: malformed token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("jwt: bad signature encoding")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, errors.New("jwt: signature mismatch")
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("jwt: bad header")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(header, &hdr); err != nil || hdr.Alg != "HS256" {
		return nil, fmt.Errorf("jwt: unsupported alg %q", hdr.Alg)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("jwt: bad payload")
	}
	var c JWTClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, errors.New("jwt: bad claims")
	}
	now := time.Now().Unix()
	if c.Exp != 0 && now > c.Exp {
		return nil, errors.New("jwt: token expired")
	}
	if c.Nbf != 0 && now < c.Nbf {
		return nil, errors.New("jwt: token not valid yet")
	}
	return &c, nil
}

// SignJWT creates an HS256 JWT with the given claims (daemon-internal use).
func SignJWT(secret string, claims map[string]interface{}) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	pB64 := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(header + "." + pB64))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return header + "." + pB64 + "." + sig, nil
}
