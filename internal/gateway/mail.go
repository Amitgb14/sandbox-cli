package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Sending invites by email.
//
// Without it the admin sends an invite's link themselves. With it the
// gateway sends the message when the admin asks, through an SMTP relay the
// operator names: their own mail server, or an email-sending service's relay.
// Only the link's address comes from the operator (--studio-url); the
// recipient is the invite's own address, already checked to be one, so a
// request cannot add a header or a recipient.
//
// The relay is reached over TLS: implicit on port 465, STARTTLS anywhere
// else, and a relay that offers neither is refused unless it is on loopback
// (a local mail server). The password is read from a file, never a flag, and
// appears in no log; neither does a link, which holds the invite's token.

// MailConfig is how the gateway sends email; nil sends none.
type MailConfig struct {
	Host string
	Port int // 465: TLS from the start; anything else: STARTTLS
	// User and Password authenticate to the relay; empty User is none.
	User     string
	Password string
	// From is the sender, "Name <address>" or an address.
	From string
	// StudioURL is hosted Studio's public address, which every link starts
	// with: https://studio.example.com.
	StudioURL string
	// Send, when set, is used instead of SMTP (tests).
	Send func(to, subject, body string) error
}

// Check says what is wrong with c, or nil.
func (c *MailConfig) Check() error {
	switch {
	case c.Host == "":
		return errors.New("--smtp-host is needed to send email")
	case c.Port <= 0 || c.Port > 65535:
		return fmt.Errorf("--smtp-port %d: 1 to 65535", c.Port)
	case c.User != "" && c.Password == "":
		return errors.New("--smtp-user needs --smtp-password-file")
	}
	if _, err := mail.ParseAddress(c.From); err != nil || strings.ContainsAny(c.From, "\r\n") {
		return fmt.Errorf("--mail-from %q: want an address, or Name <address>", c.From)
	}
	base, err := api.InviteBase(c.StudioURL)
	if err != nil {
		return fmt.Errorf("--studio-url: %w", err)
	}
	c.StudioURL = base
	return nil
}

// inviteLink is the link that opens the invite in hosted Studio.
func (c *MailConfig) inviteLink(token string) string {
	return c.StudioURL + "/#invite=" + token
}

// inviteMessage is an invite's subject and body.
func (c *MailConfig) inviteMessage(iv inviteRecord, token string) (string, string) {
	until := iv.Expires.UTC().Format("Mon 2 Jan 2006 15:04 MST")
	var b strings.Builder
	subject := "Your invite to Sandbox Studio"
	b.WriteString("Hello,\n\n")
	if iv.Key != "" {
		subject = "Set a new password for Sandbox Studio"
		b.WriteString("A new password was asked for your Sandbox Studio account (" + iv.User + "). Open this link to choose it:\n\n")
	} else {
		b.WriteString("You are invited to Sandbox Studio as " + iv.User + ". Open this link to choose a password and sign in:\n\n")
	}
	b.WriteString(c.inviteLink(token) + "\n\n")
	b.WriteString("The link works once, until " + until + ".\n")
	b.WriteString("After that, sign in at " + c.StudioURL + " with " + iv.Email + " and your password.\n\n")
	b.WriteString("If you did not expect this email, ignore it: nothing happens until the link is opened.\n")
	return subject, b.String()
}

// send delivers one plain-text message to one address.
func (c *MailConfig) send(ctx context.Context, to, subject, body string) error {
	if c.Send != nil {
		return c.Send(to, subject, body)
	}
	msg, err := c.message(to, subject, body)
	if err != nil {
		return err
	}
	from, _ := mail.ParseAddress(c.From)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	tc := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	if c.Port == 465 {
		conn, err = (&tls.Dialer{Config: tc}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("reaching the mail relay: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	cl, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("the mail relay: %w", err)
	}
	defer cl.Close()
	if err := cl.Hello("localhost"); err != nil {
		return fmt.Errorf("the mail relay: %w", err)
	}
	if c.Port != 465 {
		if ok, _ := cl.Extension("STARTTLS"); ok {
			if err := cl.StartTLS(tc); err != nil {
				return fmt.Errorf("the mail relay's TLS: %w", err)
			}
		} else if !loopbackName(c.Host) {
			// The password and the link would cross the network in the clear.
			return errors.New("the mail relay offers no TLS; use port 465 or a relay with STARTTLS")
		}
	}
	if c.User != "" {
		if err := cl.Auth(smtp.PlainAuth("", c.User, c.Password, c.Host)); err != nil {
			return fmt.Errorf("signing in to the mail relay: %w", err)
		}
	}
	if err := cl.Mail(from.Address); err != nil {
		return fmt.Errorf("the mail relay refused the sender: %w", err)
	}
	if err := cl.Rcpt(to); err != nil {
		return fmt.Errorf("the mail relay refused the address: %w", err)
	}
	w, err := cl.Data()
	if err != nil {
		return fmt.Errorf("the mail relay: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("the mail relay: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("the mail relay did not take the message: %w", err)
	}
	return cl.Quit()
}

// message is the whole RFC 5322 message. A body of plain ASCII in short
// lines — every invite's, since its addresses and names are checked to be
// ASCII — goes as it is, so the link is one unbroken line. Quoted-printable
// would break it at 76 characters and write its = as =3D: a reader decodes
// that, but a relay that rewrites links, or a client that finds them in
// the raw text, took the first line for the whole link, and the invite's
// token arrived cut short. Anything else is quoted-printable.
func (c *MailConfig) message(to, subject, body string) ([]byte, error) {
	if strings.ContainsAny(to+subject, "\r\n") {
		return nil, errors.New("a line break in a header")
	}
	from, err := mail.ParseAddress(c.From)
	if err != nil {
		return nil, err
	}
	var id [12]byte
	_, _ = rand.Read(id[:])
	domain := from.Address[strings.LastIndexByte(from.Address, '@')+1:]
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from.String())
	fmt.Fprintf(&b, "To: %s\r\n", (&mail.Address{Address: to}).String())
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id[:]), domain)
	crlf := strings.ReplaceAll(body, "\n", "\r\n")
	if plain7bit(body) {
		b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=us-ascii\r\nContent-Transfer-Encoding: 7bit\r\n\r\n")
		b.WriteString(crlf)
		return b.Bytes(), nil
	}
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(crlf)); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// emailInvite sends an invite just made, filling in what came of it. A
// failure leaves the invite as it is: the admin still has its link.
func (g *Gateway) emailInvite(ctx context.Context, out *api.CreatedInvite, iv inviteRecord, token string) {
	subject, body := g.cfg.Mail.inviteMessage(iv, token)
	if err := g.cfg.Mail.send(ctx, iv.Email, subject, body); err != nil {
		g.logf("invite %s: emailing it failed: %v", iv.ID, err)
		out.EmailError = err.Error()
		return
	}
	g.logf("invite %s emailed", iv.ID)
	out.Emailed = true
}

// loopbackName reports whether host names this machine.
func loopbackName(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// plain7bit reports whether body may be sent as 7bit: ASCII, no NUL or CR,
// no line over 998 characters, and none that is a lone "." (which SMTP's
// DATA would take as the end; net/smtp escapes it, but there is no need).
func plain7bit(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if len(line) > 998 || line == "." {
			return false
		}
		for i := 0; i < len(line); i++ {
			if c := line[i]; c == 0 || c == '\r' || c >= 0x80 {
				return false
			}
		}
	}
	return true
}
