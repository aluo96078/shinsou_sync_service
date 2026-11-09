package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const jwtIssuer = "shinsou-sync"

// Claims are the access token claims.
type Claims struct {
	DeviceID string `json:"did"`
	jwt.RegisteredClaims
}

// Issuer signs and verifies access tokens.
type Issuer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	ttl  time.Duration
}

// NewIssuer creates an Issuer.
func NewIssuer(priv ed25519.PrivateKey, ttl time.Duration) *Issuer {
	return &Issuer{priv: priv, pub: priv.Public().(ed25519.PublicKey), ttl: ttl}
}

// Sign returns a token for the device and its expiry.
func (i *Issuer) Sign(userID, deviceID uuid.UUID, now time.Time) (string, time.Time, error) {
	exp := now.Add(i.ttl)
	claims := Claims{
		DeviceID: deviceID.String(),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    jwtIssuer,
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(i.priv)
	return s, exp, err
}

// Parse verifies the signature and expiry and returns the user and device ids.
func (i *Issuer) Parse(token string, now time.Time) (userID, deviceID uuid.UUID, expires time.Time, err error) {
	var c Claims
	_, err = jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return i.pub, nil },
		jwt.WithValidMethods([]string{"EdDSA"}),
		jwt.WithIssuer(jwtIssuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return now }),
		jwt.WithLeeway(5*time.Second),
	)
	if err != nil {
		return uuid.Nil, uuid.Nil, time.Time{}, err
	}
	userID, err = uuid.Parse(c.Subject)
	if err != nil {
		return uuid.Nil, uuid.Nil, time.Time{}, errors.New("bad subject")
	}
	deviceID, err = uuid.Parse(c.DeviceID)
	if err != nil {
		return uuid.Nil, uuid.Nil, time.Time{}, errors.New("bad device id")
	}
	return userID, deviceID, c.ExpiresAt.Time, nil
}

// NewRefreshToken returns an opaque token and the hash that is stored.
func NewRefreshToken() (token string, hash []byte) {
	var b [32]byte
	_, _ = rand.Read(b[:])
	token = "shr_" + base64.RawURLEncoding.EncodeToString(b[:])
	return token, HashRefreshToken(token)
}

// HashRefreshToken is how refresh tokens are stored.
func HashRefreshToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
