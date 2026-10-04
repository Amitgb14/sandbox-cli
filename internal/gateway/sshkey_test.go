package gateway

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"math/big"
	"strings"
	"testing"
)

// wire builds an SSH wire-format blob from strings.
func wire(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(p)))
		out = append(out, n[:]...)
		out = append(out, p...)
	}
	return out
}

func line(typ string, blob []byte, comment string) string {
	s := typ + " " + base64.StdEncoding.EncodeToString(blob)
	if comment != "" {
		s += " " + comment
	}
	return s
}

func ed25519Blob(t *testing.T) []byte {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return wire([]byte("ssh-ed25519"), pub)
}

func ed25519Line(t *testing.T, comment string) string {
	return line("ssh-ed25519", ed25519Blob(t), comment)
}

func ecdsaBlob(t *testing.T, typ, curve string, c ecdh.Curve, app string) []byte {
	t.Helper()
	k, err := c.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	parts := [][]byte{[]byte(typ), []byte(curve), k.PublicKey().Bytes()}
	if app != "" {
		parts = append(parts, []byte(app))
	}
	return wire(parts...)
}

// mpint is an SSH mpint: big-endian, with a leading zero when the top bit is set.
func mpint(b *big.Int) []byte {
	out := b.Bytes()
	if len(out) > 0 && out[0]&0x80 != 0 {
		out = append([]byte{0}, out...)
	}
	return out
}

func rsaBlob(t *testing.T, bits int) []byte {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}
	return wire([]byte("ssh-rsa"), mpint(big.NewInt(int64(k.E))), mpint(k.N))
}

func TestParseAuthorizedKeyAccepts(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	for _, c := range []struct {
		name string
		line string
	}{
		{"ed25519", ed25519Line(t, "alice@laptop")},
		{"ed25519 no comment", ed25519Line(t, "")},
		{"ed25519 trailing newline", ed25519Line(t, "x") + "\n"},
		{"ecdsa p256", line("ecdsa-sha2-nistp256", ecdsaBlob(t, "ecdsa-sha2-nistp256", "nistp256", ecdh.P256(), ""), "")},
		{"ecdsa p384", line("ecdsa-sha2-nistp384", ecdsaBlob(t, "ecdsa-sha2-nistp384", "nistp384", ecdh.P384(), ""), "")},
		{"ecdsa p521", line("ecdsa-sha2-nistp521", ecdsaBlob(t, "ecdsa-sha2-nistp521", "nistp521", ecdh.P521(), ""), "")},
		{"sk ecdsa", line("sk-ecdsa-sha2-nistp256@openssh.com", ecdsaBlob(t, "sk-ecdsa-sha2-nistp256@openssh.com", "nistp256", ecdh.P256(), "ssh:"), "yubikey")},
		{"sk ed25519", line("sk-ssh-ed25519@openssh.com", wire([]byte("sk-ssh-ed25519@openssh.com"), pub, []byte("ssh:")), "")},
		{"rsa 2048", line("ssh-rsa", rsaBlob(t, 2048), "old")},
	} {
		t.Run(c.name, func(t *testing.T) {
			k, err := ParseAuthorizedKey(c.line)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(k.Blob)
			if k.Fingerprint != "SHA256:"+base64.RawStdEncoding.EncodeToString(sum[:]) {
				t.Errorf("fingerprint %s", k.Fingerprint)
			}
			if strings.ContainsAny(k.Line(), "\n") || !strings.HasPrefix(c.line, k.Line()) {
				t.Errorf("Line() = %q", k.Line())
			}
		})
	}
}

// A known key and the fingerprint ssh-keygen -l prints for it.
func TestFingerprintMatchesSSHKeygen(t *testing.T) {
	// The ed25519 public key 00 01 02 … 1f; the fingerprint is what
	// OpenSSH's ssh-keygen -lf printed for this line.
	k, err := ParseAuthorizedKey("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f vec")
	if err != nil {
		t.Fatal(err)
	}
	if want := "SHA256:ZkAslGjFiUHdGf/WUL8rQvkib4PTvQatUV0OUQSncCA"; k.Fingerprint != want {
		t.Fatalf("fingerprint %s, want %s", k.Fingerprint, want)
	}
	if k.Comment != "vec" {
		t.Errorf("comment %q", k.Comment)
	}
}

// Options are instructions to an OpenSSH server; the gateway honours none,
// so a key carrying one is refused rather than stored meaning less than it
// says.
func TestParseAuthorizedKeyRefuses(t *testing.T) {
	good := ed25519Line(t, "me")
	blob := ed25519Blob(t)
	for _, c := range []struct {
		name, line, want string
	}{
		{"command option", `command="/bin/sh" ` + good, "options"},
		{"from option", `from="10.0.0.0/8" ` + good, "options"},
		{"flag option", "no-pty " + good, "options"},
		{"restrict", "restrict,command=\"x\" " + good, "options"},
		{"cert-authority", "cert-authority " + good, "options"},
		{"two lines", good + "\n" + good, "more than one line"},
		{"carriage return inside", good + "\r" + good, "more than one line"},
		{"control character in comment", ed25519Line(t, "evil\x1b[2J"), "control character"},
		{"unknown type", line("ssh-dss", wire([]byte("ssh-dss"), []byte("x")), ""), "not accepted"},
		{"no key", "ssh-ed25519", "no key"},
		{"empty", "  ", "no key"},
		{"bad base64", "ssh-ed25519 !!!notbase64", "base64"},
		{"type mismatch", line("ssh-ed25519", wire([]byte("ssh-rsa"), make([]byte, 32)), ""), "not a ssh-ed25519"},
		{"short ed25519", line("ssh-ed25519", wire([]byte("ssh-ed25519"), make([]byte, 31)), ""), "want 32"},
		{"trailing bytes", line("ssh-ed25519", append(blob, 0), ""), "trailing"},
		{"truncated", line("ssh-ed25519", blob[:20], ""), "truncated"},
		{"ecdsa wrong curve", line("ecdsa-sha2-nistp256", ecdsaBlob(t, "ecdsa-sha2-nistp256", "nistp384", ecdh.P384(), ""), ""), "curve"},
		{"ecdsa off curve", line("ecdsa-sha2-nistp256", wire([]byte("ecdsa-sha2-nistp256"), []byte("nistp256"), append([]byte{4}, make([]byte, 64)...)), ""), "not on the curve"},
		{"sk without ssh: app", line("sk-ssh-ed25519@openssh.com", wire([]byte("sk-ssh-ed25519@openssh.com"), make([]byte, 32), []byte("web:")), ""), "application"},
		{"rsa 1024", line("ssh-rsa", rsaBlob(t, 1024), ""), "at least 2048"},
		{"rsa even exponent", line("ssh-rsa", wire([]byte("ssh-rsa"), []byte{4}, mpint(new(big.Int).Lsh(big.NewInt(1), 2047))), ""), "exponent"},
		{"too long", "ssh-ed25519 " + strings.Repeat("A", maxAuthorizedKey), "longer than"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseAuthorizedKey(c.line)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v; want one mentioning %q", err, c.want)
			}
		})
	}
}
