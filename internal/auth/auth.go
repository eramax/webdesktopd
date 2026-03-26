package auth

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/ssh"
)

const DefaultSSHAddr = "localhost:22"
const DefaultJWTTTL = 24 * time.Hour

// Claims is the JWT claims structure.
type Claims struct {
	jwt.RegisteredClaims
}

// Authenticator validates credentials and issues JWTs.
type Authenticator struct {
	sshAddr   string
	jwtSecret []byte
	jwtTTL    time.Duration
}

// New creates a new Authenticator.
func New(sshAddr string, jwtSecret []byte, jwtTTL time.Duration) *Authenticator {
	if sshAddr == "" {
		sshAddr = DefaultSSHAddr
	}
	if jwtTTL <= 0 {
		jwtTTL = DefaultJWTTTL
	}
	return &Authenticator{
		sshAddr:   sshAddr,
		jwtSecret: jwtSecret,
		jwtTTL:    jwtTTL,
	}
}

// dialSSH establishes an SSH connection using the provided config, respecting ctx deadline.
func (a *Authenticator) dialSSH(ctx context.Context, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	// Use context deadline for TCP dial timeout.
	deadline, ok := ctx.Deadline()
	if ok {
		cfg.Timeout = time.Until(deadline)
		if cfg.Timeout <= 0 {
			return nil, fmt.Errorf("context deadline already exceeded")
		}
	} else {
		cfg.Timeout = 10 * time.Second
	}

	// Dial with context awareness.
	d := &net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", a.sshAddr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", a.sshAddr, err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, a.sshAddr, cfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ssh handshake: %w", err)
	}

	return ssh.NewClient(sshConn, chans, reqs), nil
}

// issueJWT creates and returns a signed JWT for the given username.
func (a *Authenticator) issueJWT(username string) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(a.jwtTTL)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(a.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}
	return signed, nil
}

// Authenticate dials sshAddr with username/password, on success returns a signed JWT.
// Returns an error if credentials are invalid.
func (a *Authenticator) Authenticate(ctx context.Context, username, password string) (string, error) {
	cfg := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // intentional for local loopback auth
	}

	client, err := a.dialSSH(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("authentication failed: %w", err)
	}
	client.Close()

	return a.issueJWT(username)
}

// AuthenticateKey dials sshAddr with username and a PEM-encoded private key, returns JWT.
func (a *Authenticator) AuthenticateKey(ctx context.Context, username string, privateKeyPEM []byte) (string, error) {
	signer, err := ssh.ParsePrivateKey(privateKeyPEM)
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}

	cfg := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // intentional for local loopback auth
	}

	client, err := a.dialSSH(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("authentication failed: %w", err)
	}
	client.Close()

	return a.issueJWT(username)
}

// ValidateToken validates a JWT string, returns the username (sub claim) on success.
func (a *Authenticator) ValidateToken(tokenStr string) (string, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return a.jwtSecret, nil
	})
	if err != nil {
		return "", fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return "", fmt.Errorf("invalid token claims")
	}

	username := claims.Subject
	if username == "" {
		return "", fmt.Errorf("token missing sub claim")
	}

	return username, nil
}
