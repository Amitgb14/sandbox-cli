package gateway

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

type sentMail struct{ to, subject, body string }

// mailGateway is a gateway that emails through a recorder, failing while
// fail is set.
func mailGateway(t *testing.T) (*testGateway, *[]sentMail, *bool) {
	t.Helper()
	var mu sync.Mutex
	var sent []sentMail
	fail := false
	tg := startGateway(t, func(c *Config) {
		c.Mail = &MailConfig{Host: "relay.example.com", Port: 587, From: "Studio <invites@example.com>", StudioURL: "https://studio.example.com",
			Send: func(to, subject, body string) error {
				mu.Lock()
				defer mu.Unlock()
				if fail {
					return errors.New("the relay said no")
				}
				sent = append(sent, sentMail{to, subject, body})
				return nil
			}}
	}, startNode(t, "n1"))
	return tg, &sent, &fail
}

// The gateway emails an invite's link, and a reset's, to the invite's own
// address; a failure to send leaves the invite usable, and says why.
func TestInvitesAreEmailed(t *testing.T) {
	tg, sent, fail := mailGateway(t)
	ctx := ctxT(t)
	if list, err := tg.admin.Invites(ctx); err != nil || !list.CanEmail {
		t.Fatalf("can_email: %+v, %v", list, err)
	}
	iv, err := tg.admin.CreateInvite(ctx, api.CreateInviteRequest{Email: "Bob@Example.com", User: "bob", Scopes: demoScopes, SendEmail: true})
	if err != nil || !iv.Emailed {
		t.Fatalf("create: %+v, %v", iv, err)
	}
	if len(*sent) != 1 {
		t.Fatalf("sent: %+v", *sent)
	}
	m := (*sent)[0]
	if m.to != "bob@example.com" || !strings.Contains(m.body, "https://studio.example.com/#invite="+iv.Token+"\n") || !strings.Contains(m.subject, "invite") {
		t.Errorf("the email: %+v", m)
	}
	if _, err := tg.anon().Register(ctx, api.RegisterRequest{Token: iv.Token, Password: bobPassword}); err != nil {
		t.Fatal(err)
	}

	acct, _ := tg.store.Account("bob@example.com")
	reset, err := tg.admin.ResetPassword(ctx, acct.ID, true)
	if err != nil || !reset.Emailed || len(*sent) != 2 || !strings.Contains((*sent)[1].body, reset.Token) || !strings.Contains((*sent)[1].subject, "new password") {
		t.Errorf("reset: %+v, %v, %+v", reset, err, *sent)
	}

	*fail = true
	iv, err = tg.admin.CreateInvite(ctx, api.CreateInviteRequest{Email: "carol@example.com", User: "carol", Scopes: demoScopes, SendEmail: true})
	if err != nil || iv.Emailed || !strings.Contains(iv.EmailError, "the relay said no") {
		t.Fatalf("a failed send: %+v, %v", iv, err)
	}
	if _, err := tg.anon().InviteFor(ctx, iv.Token); err != nil {
		t.Errorf("an invite whose email failed: %v", err)
	}
	// Not asked to send, nothing is sent.
	*fail = false
	if _, err := tg.admin.CreateInvite(ctx, api.CreateInviteRequest{Email: "dan@example.com", User: "dan", Scopes: demoScopes}); err != nil || len(*sent) != 2 {
		t.Errorf("an invite not to be emailed: %v, %d sent", err, len(*sent))
	}
}

// A gateway with no relay says so, and refuses to be asked to send.
func TestNoMailWithoutARelay(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	if list, _ := tg.admin.Invites(ctx); list.CanEmail {
		t.Error("can_email without a relay")
	}
	_, err := tg.admin.CreateInvite(ctx, api.CreateInviteRequest{Email: "bob@example.com", User: "bob", Scopes: demoScopes, SendEmail: true})
	wantCode(t, err, api.CodeInvalidRequest)
	if len(tg.store.Invites()) != 0 {
		t.Error("an invite was made though it could not be sent")
	}
}

func TestMailConfigCheck(t *testing.T) {
	good := func() *MailConfig {
		return &MailConfig{Host: "smtp.example.com", Port: 465, User: "u", Password: "p", From: "Studio <a@example.com>", StudioURL: "https://studio.example.com/"}
	}
	c := good()
	if err := c.Check(); err != nil || c.StudioURL != "https://studio.example.com" {
		t.Fatalf("a good config: %v, %q", err, c.StudioURL)
	}
	for name, edit := range map[string]func(*MailConfig){
		"no host":             func(c *MailConfig) { c.Host = "" },
		"a user, no password": func(c *MailConfig) { c.Password = "" },
		"a bad sender":        func(c *MailConfig) { c.From = "not an address" },
		"a sender with a CR":  func(c *MailConfig) { c.From = "a@example.com\r\nBcc: x@example.com" },
		"no Studio address":   func(c *MailConfig) { c.StudioURL = "" },
		"a Studio with a path": func(c *MailConfig) {
			c.StudioURL = "https://studio.example.com/x?y"
		},
	} {
		c := good()
		edit(c)
		if err := c.Check(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// fakeSMTP is a relay on loopback, without TLS (the client allows that on
// loopback only), that takes one message with AUTH PLAIN.
type fakeSMTP struct {
	ln   net.Listener
	auth string
	from string
	rcpt []string
	data string
	done chan struct{}
}

func startSMTP(t *testing.T) *fakeSMTP {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, done: make(chan struct{})}
	t.Cleanup(func() { ln.Close() })
	go func() {
		defer close(f.done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		say := func(s string) { _, _ = io.WriteString(conn, s+"\r\n") }
		say("220 fake ESMTP")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0]); cmd {
			case "EHLO", "HELO":
				say("250-fake")
				say("250 AUTH PLAIN")
			case "AUTH":
				f.auth = strings.TrimPrefix(line, "AUTH PLAIN ")
				say("235 ok")
			case "MAIL":
				f.from = line
				say("250 ok")
			case "RCPT":
				f.rcpt = append(f.rcpt, line)
				say("250 ok")
			case "DATA":
				say("354 go on")
				var b strings.Builder
				for {
					l, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if l == ".\r\n" {
						break
					}
					b.WriteString(l)
				}
				f.data = b.String()
				say("250 queued")
			case "QUIT":
				say("221 bye")
				return
			default:
				say("502 no")
			}
		}
	}()
	return f
}

// Over the wire: one recipient, the sender's address, the password only in
// AUTH, and a message whose headers are the ones written and whose body,
// decoded, holds the link.
func TestMailOverSMTP(t *testing.T) {
	f := startSMTP(t)
	_, port, _ := net.SplitHostPort(f.ln.Addr().String())
	p, _ := strconv.Atoi(port)
	c := &MailConfig{Host: "127.0.0.1", Port: p, User: "relay-user", Password: "relay-secret", From: "Sandbox Studio <invites@example.com>", StudioURL: "https://studio.example.com"}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
	iv := inviteRecord{Email: "bob@example.com", User: "bob"}
	subject, body := c.inviteMessage(iv, "sgi_token")
	if err := c.send(context.Background(), iv.Email, subject, body); err != nil {
		t.Fatal(err)
	}
	<-f.done
	if auth, _ := base64.StdEncoding.DecodeString(f.auth); string(auth) != "\x00relay-user\x00relay-secret" {
		t.Errorf("AUTH: %q", auth)
	}
	if f.from != "MAIL FROM:<invites@example.com>" || len(f.rcpt) != 1 || f.rcpt[0] != "RCPT TO:<bob@example.com>" {
		t.Errorf("envelope: %q %q", f.from, f.rcpt)
	}
	msg, err := mail.ReadMessage(strings.NewReader(f.data))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("To") != "<bob@example.com>" || !strings.Contains(msg.Header.Get("From"), "invites@example.com") || msg.Header.Get("Subject") != subject {
		t.Errorf("headers: %v", msg.Header)
	}
	// The link whole, on one line of the raw message: not wrapped, not
	// encoded, so nothing between here and the reader can cut it.
	if msg.Header.Get("Content-Transfer-Encoding") != "7bit" || !strings.Contains(f.data, "\r\n"+c.inviteLink("sgi_token")+"\r\n") {
		t.Errorf("the link is not one raw line: %q", f.data)
	}
	if strings.Contains(f.data, "relay-secret") {
		t.Error("the password is in the message")
	}
	if _, err := c.message("bob@example.com", "x\r\nBcc: eve@example.com", "b"); err == nil {
		t.Error("a header with a line break was written")
	}
}

// A real token makes a link longer than quoted-printable's 76 characters;
// it is still one line. A body that is not ASCII is quoted-printable.
func TestTheLinkIsOneLine(t *testing.T) {
	c := &MailConfig{From: "Studio <invites@example.com>", StudioURL: "https://localhost-0.example.ts.net"}
	token := newSecret(inviteSecretPrefix)
	subject, body := c.inviteMessage(inviteRecord{Email: "bob@example.com", User: "bob"}, token)
	msg, err := c.message("bob@example.com", subject, body)
	if err != nil {
		t.Fatal(err)
	}
	link := c.inviteLink(token)
	if len(link) <= 76 || !strings.Contains(string(msg), "\r\n"+link+"\r\n") {
		t.Errorf("the link (%d characters) is not whole on one line:\n%s", len(link), msg)
	}
	msg, _ = c.message("bob@example.com", "s", "caf\u00e9")
	if m, _ := mail.ReadMessage(strings.NewReader(string(msg))); m.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
		t.Errorf("a body that is not ASCII: %s", msg)
	}
	if b, _ := io.ReadAll(quotedprintable.NewReader(strings.NewReader(string(msg[strings.Index(string(msg), "\r\n\r\n")+4:])))); string(b) != "caf\u00e9" {
		t.Errorf("decoded: %q", b)
	}
}

// Resending gives a pending invite a new link, emailed: the old one stops
// working, the new one makes the account the invite was for, and an invite
// already used cannot be resent.
func TestResendAnInvite(t *testing.T) {
	tg, sent, _ := mailGateway(t)
	ctx := ctxT(t)
	first, err := tg.admin.CreateInvite(ctx, api.CreateInviteRequest{Email: "bob@example.com", User: "bob", Tenant: "bob", Scopes: demoScopes})
	if err != nil {
		t.Fatal(err)
	}
	again, err := tg.admin.ResendInvite(ctx, first.ID, true)
	if err != nil || !again.Emailed || again.ID != first.ID || again.Token == first.Token || again.User != "bob" {
		t.Fatalf("resend: %+v, %v", again, err)
	}
	if len(*sent) != 1 || !strings.Contains((*sent)[0].body, again.Token) || (*sent)[0].to != "bob@example.com" {
		t.Errorf("the email: %+v", *sent)
	}
	_, err = tg.anon().InviteFor(ctx, first.Token)
	wantCode(t, err, api.CodeNotFound)
	reg, err := tg.anon().Register(ctx, api.RegisterRequest{Token: again.Token, Password: bobPassword})
	if err != nil || reg.User != "bob" || reg.Tenant != "bob" {
		t.Fatalf("register with the new link: %+v, %v", reg, err)
	}
	_, err = tg.admin.ResendInvite(ctx, first.ID, false)
	wantCode(t, err, api.CodeNotFound)
	_, err = tg.user("erin").ResendInvite(ctx, first.ID, false)
	wantCode(t, err, api.CodeRefused)
}
