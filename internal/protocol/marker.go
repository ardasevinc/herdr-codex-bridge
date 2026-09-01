package protocol

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	Prefix       = "[herdr-bridge:v1"
	MaxClockSkew = 2 * time.Minute
)

type Marker struct {
	SessionID string
	Source    string
	IssuedAt  time.Time
	Nonce     string
}

func New(sessionID, source string, now time.Time) (Marker, error) {
	if !validToken(sessionID) || !validToken(source) {
		return Marker{}, errors.New("session id and source must be non-empty marker tokens")
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Marker{}, fmt.Errorf("generate nonce: %w", err)
	}
	return Marker{
		SessionID: sessionID,
		Source:    source,
		IssuedAt:  now.UTC(),
		Nonce:     base64.RawURLEncoding.EncodeToString(nonce[:]),
	}, nil
}

func (m Marker) Sign(key []byte) (string, error) {
	if len(key) < 32 {
		return "", errors.New("bridge key must contain at least 32 bytes")
	}
	if !validToken(m.SessionID) || !validToken(m.Source) || !validToken(m.Nonce) || m.IssuedAt.IsZero() {
		return "", errors.New("invalid marker fields")
	}
	payload := m.payload()
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%s session=%s source=%s issued_at=%d nonce=%s sig=%s]",
		Prefix, m.SessionID, m.Source, m.IssuedAt.UnixMilli(), m.Nonce, signature), nil
}

func ParseAndVerify(line string, key []byte, now time.Time) (Marker, error) {
	marker, err := ParseAndVerifySignature(line, key)
	if err != nil {
		return Marker{}, err
	}
	age := now.Sub(marker.IssuedAt)
	if age < -MaxClockSkew || age > MaxClockSkew {
		return Marker{}, errors.New("stale bridge marker")
	}
	return marker, nil
}

// ParseAndVerifySignature authenticates marker identity without granting it
// freshness. It is used only to create a pane-local witness claim; mapping
// still requires ParseAndVerify on a newly emitted marker.
func ParseAndVerifySignature(line string, key []byte) (Marker, error) {
	start := strings.Index(line, Prefix+" ")
	if start < 0 {
		return Marker{}, errors.New("bridge marker not found")
	}
	endOffset := strings.IndexByte(line[start:], ']')
	if endOffset < 0 {
		return Marker{}, errors.New("unterminated bridge marker")
	}
	parts := strings.Fields(line[start+len(Prefix)+1 : start+endOffset])
	values := make(map[string]string, len(parts))
	for _, part := range parts {
		key, value, ok := strings.Cut(part, "=")
		if !ok || value == "" {
			return Marker{}, errors.New("malformed bridge marker")
		}
		if _, exists := values[key]; exists {
			return Marker{}, errors.New("duplicate bridge marker field")
		}
		values[key] = value
	}
	for _, field := range []string{"session", "source", "issued_at", "nonce", "sig"} {
		if !validToken(values[field]) {
			return Marker{}, fmt.Errorf("invalid %s field", field)
		}
	}
	issuedAtMS, err := strconv.ParseInt(values["issued_at"], 10, 64)
	if err != nil {
		return Marker{}, errors.New("invalid issued_at field")
	}
	marker := Marker{
		SessionID: values["session"],
		Source:    values["source"],
		IssuedAt:  time.UnixMilli(issuedAtMS).UTC(),
		Nonce:     values["nonce"],
	}
	if len(key) < 32 {
		return Marker{}, errors.New("bridge key must contain at least 32 bytes")
	}
	provided, err := base64.RawURLEncoding.DecodeString(values["sig"])
	if err != nil {
		return Marker{}, errors.New("invalid marker signature encoding")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(marker.payload()))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return Marker{}, errors.New("invalid marker signature")
	}
	return marker, nil
}

func (m Marker) payload() string {
	return fmt.Sprintf("v1\x00%s\x00%s\x00%d\x00%s", m.SessionID, m.Source, m.IssuedAt.UnixMilli(), m.Nonce)
}

func validToken(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	return !strings.ContainsAny(value, " \t\r\n]=")
}
