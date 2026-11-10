// Package mail sends the transactional mail (address verification, password reset). Sending is
// done by a Sender; requests never wait for it, they only put a Message on a Queue.
package mail

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/aluo96078/shinsou_sync_service/internal/config"
)

// Message is one plain-text mail.
type Message struct {
	To      string // bare address, no display name
	Subject string
	Body    string
}

// Sender delivers a message. Implementations must be safe for concurrent use.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// SMTPConfig configures an SMTPSender.
type SMTPConfig struct {
	Host        string
	Port        int
	Username    string
	Password    string
	From        string // "Name <addr>" or a bare address
	TLS         string // config.SMTPStartTLS, SMTPImplicit or SMTPNone
	DialTimeout time.Duration
	IOTimeout   time.Duration // deadline for the whole conversation after connecting
}

// SMTPFromConfig builds the SMTP settings from the server configuration.
func SMTPFromConfig(c config.Config) SMTPConfig {
	return SMTPConfig{Host: c.SMTPHost, Port: c.SMTPPort, Username: c.SMTPUsername, Password: c.SMTPPassword,
		From: c.SMTPFrom, TLS: c.SMTPTLS}
}

// SMTPSender talks SMTP using only the standard library.
type SMTPSender struct {
	cfg  SMTPConfig
	from *mail.Address
}

// NewSMTPSender validates the configuration.
func NewSMTPSender(c SMTPConfig) (*SMTPSender, error) {
	if c.Host == "" {
		return nil, errors.New("smtp host is empty")
	}
	if c.Port < 1 || c.Port > 65535 {
		return nil, fmt.Errorf("smtp port %d is out of range", c.Port)
	}
	switch c.TLS {
	case config.SMTPStartTLS, config.SMTPImplicit, config.SMTPNone:
	case "":
		c.TLS = config.SMTPStartTLS
	default:
		return nil, fmt.Errorf("unknown smtp tls mode %q", c.TLS)
	}
	if err := checkHeader("from", c.From); err != nil {
		return nil, err
	}
	from, err := mail.ParseAddress(c.From)
	if err != nil {
		return nil, fmt.Errorf("smtp from: %w", err)
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = 10 * time.Second
	}
	if c.IOTimeout <= 0 {
		c.IOTimeout = 30 * time.Second
	}
	return &SMTPSender{cfg: c, from: from}, nil
}

// checkHeader rejects anything that could end a header line early (header injection).
func checkHeader(name, v string) error {
	if strings.ContainsAny(v, "\r\n\x00") {
		return fmt.Errorf("%s contains a line break", name)
	}
	return nil
}

// Build renders the RFC 5322 message. It is exported for tests.
func (s *SMTPSender) Build(m Message, now time.Time) ([]byte, string, error) {
	if err := checkHeader("recipient", m.To); err != nil {
		return nil, "", err
	}
	if err := checkHeader("subject", m.Subject); err != nil {
		return nil, "", err
	}
	to, err := mail.ParseAddress(m.To)
	if err != nil || to.Address != m.To {
		return nil, "", errors.New("recipient is not a bare email address")
	}
	var id [12]byte
	_, _ = rand.Read(id[:])
	domain := "localhost"
	if i := strings.LastIndexByte(s.from.Address, '@'); i >= 0 {
		domain = s.from.Address[i+1:]
	}
	var b strings.Builder
	hdr := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	hdr("From", s.from.String())
	hdr("To", to.Address)
	hdr("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	hdr("Date", now.UTC().Format(time.RFC1123Z))
	hdr("Message-ID", "<"+hex.EncodeToString(id[:])+"@"+domain+">")
	hdr("MIME-Version", "1.0")
	hdr("Content-Type", "text/plain; charset=utf-8")
	hdr("Content-Transfer-Encoding", "base64")
	b.WriteString("\r\n")
	enc := base64.StdEncoding.EncodeToString([]byte(strings.ReplaceAll(strings.ReplaceAll(m.Body, "\r\n", "\n"), "\n", "\r\n")))
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return []byte(b.String()), to.Address, nil
}

// plainAuth is AUTH PLAIN without net/smtp's refusal to run over an unencrypted connection: the
// operator chose `none` explicitly.
type plainAuth struct{ user, pass string }

func (a plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}

func (a plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	return nil, errors.New("unexpected server challenge")
}

// Send delivers one message, opening a connection for it.
func (s *SMTPSender) Send(ctx context.Context, m Message) error {
	data, rcpt, err := s.Build(m, time.Now())
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	nd := &net.Dialer{Timeout: s.cfg.DialTimeout}
	tlsCfg := &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	if s.cfg.TLS == config.SMTPImplicit {
		conn, err = (&tls.Dialer{NetDialer: nd, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = nd.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp connect: %w", err)
	}
	deadline := time.Now().Add(s.cfg.IOTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer c.Close()
	if s.cfg.TLS == config.SMTPStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("smtp server does not offer STARTTLS")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if s.cfg.Username != "" {
		if err := c.Auth(plainAuth{s.cfg.Username, s.cfg.Password}); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err := c.Rcpt(rcpt); err != nil {
		return fmt.Errorf("smtp RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp end of data: %w", err)
	}
	return c.Quit()
}
