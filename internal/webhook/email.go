package webhook

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Mailer sends one rendered email for an email-format endpoint. The default
// is an SMTP client behind the same destination checks as HTTP delivery;
// tests inject a stub.
type Mailer interface {
	Send(ctx context.Context, ep Endpoint, msg EmailMessage) error
}

// EmailMessage is one rendered plain-text email.
type EmailMessage struct {
	Subject string
	Body    string
}

// validateEmailEndpoint checks the email envelope: a parseable sender, at
// least one parseable recipient, and a password whenever a user is named.
// net/mail rejects CR and LF, so an address can never inject a header.
func validateEmailEndpoint(ep Endpoint) error {
	if _, err := mail.ParseAddress(ep.From); err != nil {
		return ErrConfigDiagnostic
	}
	if len(ep.To) == 0 {
		return ErrConfigDiagnostic
	}
	for _, to := range ep.To {
		if _, err := mail.ParseAddress(to); err != nil {
			return ErrConfigDiagnostic
		}
	}
	if strings.ContainsAny(ep.Username, "\r\n") {
		return ErrConfigDiagnostic
	}
	if ep.Username != "" && len(ep.Secret) == 0 {
		return ErrConfigDiagnostic
	}
	return nil
}

// renderEmail builds the plain-text email for a derived event.
func renderEmail(_ Endpoint, n Notification) EmailMessage {
	subject := "Beamfall: " + n.headline()
	if n.Title != "" {
		subject += ": " + n.Title
	}
	var body strings.Builder
	body.WriteString(n.message())
	body.WriteString("\n")
	if n.MediaClass != "" {
		body.WriteString("\nType: " + n.MediaClass)
	}
	if n.ArtworkURL != "" {
		body.WriteString("\nArtwork: " + n.ArtworkURL)
	}
	if !n.OccurredAt.IsZero() {
		body.WriteString("\nWhen: " + n.OccurredAt.UTC().Format(time.RFC1123))
	}
	body.WriteString("\n\nSent by your Beamfall server.\n")
	return EmailMessage{Subject: truncateRunes(subject, 200), Body: body.String()}
}

// smtpMailer is the default Mailer.
type smtpMailer struct{}

// Send delivers msg over SMTP. smtps:// uses implicit TLS; smtp:// requires
// STARTTLS, except for a local server reached without a password. A password
// is never sent over an unencrypted connection.
func (smtpMailer) Send(ctx context.Context, ep Endpoint, msg EmailMessage) error {
	allowLocal, err := endpointAllowsLocal(ep)
	if err != nil {
		return ErrRequestDiagnostic
	}
	u, err := url.Parse(ep.URL)
	if err != nil {
		return ErrRequestDiagnostic
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "587"
		if u.Scheme == "smtps" {
			port = "465"
		}
	}
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			return checkDialAddress(address, allowLocal)
		},
	}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return ErrTransportDiagnostic
	}
	deadline := time.Now().Add(defaultHTTPTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	encrypted := false
	if u.Scheme == "smtps" {
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return ErrTransportDiagnostic
		}
		conn = tlsConn
		encrypted = true
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return ErrTransportDiagnostic
	}
	defer func() { _ = client.Close() }()
	if !encrypted {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				return ErrTransportDiagnostic
			}
			encrypted = true
		}
	}
	if !encrypted && (ep.Username != "" || !allowLocal) {
		return ErrTransportDiagnostic
	}
	if ep.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", ep.Username, string(ep.Secret), host)); err != nil {
			return smtpError(err)
		}
	}
	if err := client.Mail(addressOnly(ep.From)); err != nil {
		return smtpError(err)
	}
	for _, to := range ep.To {
		if err := client.Rcpt(addressOnly(to)); err != nil {
			return smtpError(err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return smtpError(err)
	}
	if _, err := w.Write(buildEmail(ep, msg, time.Now())); err != nil {
		_ = w.Close()
		return ErrTransportDiagnostic
	}
	if err := w.Close(); err != nil {
		return smtpError(err)
	}
	_ = client.Quit()
	return nil
}

// smtpError maps a server reply to the status diagnostic and anything else to
// the transport diagnostic, never carrying the server's text (it can echo the
// user name).
func smtpError(err error) error {
	var reply *textproto.Error
	if errors.As(err, &reply) {
		return ErrStatusDiagnostic
	}
	return ErrTransportDiagnostic
}

func addressOnly(raw string) string {
	addr, err := mail.ParseAddress(raw)
	if err != nil {
		return raw
	}
	return addr.Address
}

// buildEmail serializes the message: fixed headers with an encoded subject,
// and a quoted-printable UTF-8 body.
func buildEmail(ep Endpoint, msg EmailMessage, now time.Time) []byte {
	var buf bytes.Buffer
	writeHeader := func(name, value string) {
		fmt.Fprintf(&buf, "%s: %s\r\n", name, value)
	}
	writeHeader("From", formatAddress(ep.From))
	to := make([]string, 0, len(ep.To))
	for _, addr := range ep.To {
		to = append(to, formatAddress(addr))
	}
	writeHeader("To", strings.Join(to, ", "))
	writeHeader("Subject", mime.QEncoding.Encode("utf-8", stripControls(msg.Subject)))
	writeHeader("Date", now.UTC().Format(time.RFC1123Z))
	writeHeader("MIME-Version", "1.0")
	writeHeader("Content-Type", "text/plain; charset=utf-8")
	writeHeader("Content-Transfer-Encoding", "quoted-printable")
	writeHeader("Auto-Submitted", "auto-generated")
	buf.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&buf)
	_, _ = qp.Write([]byte(strings.ReplaceAll(msg.Body, "\n", "\r\n")))
	_ = qp.Close()
	return buf.Bytes()
}

func formatAddress(raw string) string {
	addr, err := mail.ParseAddress(raw)
	if err != nil {
		return stripControls(raw)
	}
	return addr.String()
}

// stripControls replaces control characters (CR and LF included) with spaces
// so no value can end a header line early.
func stripControls(value string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
}
