package gateway

import (
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// The SSH keys a user registers are parsed here with the standard library
// alone: the SSH package is the SSH server's, and a key that is stored has
// to be understood before any server sees it.
//
// An authorized_keys line may carry options before the key — command=,
// from=, permitopen=, environment=. They are instructions to an OpenSSH
// server, and a key that arrives with one is a key whose owner expects it to
// be honoured. The gateway honours none, so it refuses them rather than
// storing a key that would quietly mean less than it says.

// maxAuthorizedKey bounds one line. The largest key accepted, a 16384-bit RSA
// key, is about 2.8 KB of base64.
const maxAuthorizedKey = 8 << 10

// ParsedKey is a public key from one authorized_keys line.
type ParsedKey struct {
	Type    string
	Blob    []byte // the wire-format public key
	Comment string
	// Fingerprint is SHA256: and the unpadded base64 of the blob's SHA-256,
	// as ssh-keygen -l prints it.
	Fingerprint string
}

// Line is the key as an authorized_keys line with no options.
func (k ParsedKey) Line() string {
	s := k.Type + " " + base64.StdEncoding.EncodeToString(k.Blob)
	if k.Comment != "" {
		s += " " + k.Comment
	}
	return s
}

// Fingerprint is the SHA256 fingerprint of a wire-format public key.
func Fingerprint(blob []byte) string {
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// keyTypes are the types accepted, mapped to their structural check.
var keyTypes = map[string]func(*wireReader) error{
	"ssh-ed25519":                        checkEd25519(false),
	"sk-ssh-ed25519@openssh.com":         checkEd25519(true),
	"ecdsa-sha2-nistp256":                checkECDSA("nistp256", ecdh.P256(), false),
	"ecdsa-sha2-nistp384":                checkECDSA("nistp384", ecdh.P384(), false),
	"ecdsa-sha2-nistp521":                checkECDSA("nistp521", ecdh.P521(), false),
	"sk-ecdsa-sha2-nistp256@openssh.com": checkECDSA("nistp256", ecdh.P256(), true),
	"ssh-rsa":                            checkRSA,
}

// minRSABits is the smallest RSA modulus accepted. Smaller keys are within
// reach of a well-funded attacker, and OpenSSH itself refuses below 1024.
const minRSABits = 2048

// ParseAuthorizedKey parses one authorized_keys line: a key type, the
// base64 key and an optional comment. Options, a second line, an unknown
// key type and a key whose bytes do not match its type are refused.
func ParseAuthorizedKey(line string) (ParsedKey, error) {
	if len(line) > maxAuthorizedKey {
		return ParsedKey{}, fmt.Errorf("the key is longer than %d bytes", maxAuthorizedKey)
	}
	line = strings.TrimRight(line, "\r\n")
	line = strings.TrimSpace(line)
	if line == "" {
		return ParsedKey{}, errors.New("no key given")
	}
	for _, r := range line {
		if r == '\n' || r == '\r' {
			return ParsedKey{}, errors.New("one key at a time: the text has more than one line")
		}
		// The comment is shown back in listings; a control character there
		// is a way to write to the terminal of whoever reads one.
		if r < 0x20 && r != '\t' || r == 0x7f {
			return ParsedKey{}, errors.New("the key has a control character")
		}
	}
	fields := strings.Fields(line)
	check, ok := keyTypes[fields[0]]
	if !ok {
		if looksLikeOptions(fields[0]) {
			return ParsedKey{}, fmt.Errorf("authorized_keys options (%s) are not accepted; give the key alone", optionName(fields[0]))
		}
		return ParsedKey{}, fmt.Errorf("key type %q is not accepted; use ssh-ed25519, ecdsa-sha2-nistp256/384/521, a security-key type or ssh-rsa", fields[0])
	}
	if len(fields) < 2 {
		return ParsedKey{}, errors.New("the key has a type but no key")
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return ParsedKey{}, errors.New("the key is not valid base64")
	}
	r := &wireReader{b: blob}
	typ, err := r.str()
	if err != nil || string(typ) != fields[0] {
		return ParsedKey{}, fmt.Errorf("the key's bytes are not a %s key", fields[0])
	}
	if err := check(r); err != nil {
		return ParsedKey{}, fmt.Errorf("%s key: %w", fields[0], err)
	}
	if len(r.b) != 0 {
		return ParsedKey{}, fmt.Errorf("%s key: trailing bytes", fields[0])
	}
	return ParsedKey{
		Type:        fields[0],
		Blob:        blob,
		Comment:     strings.Join(fields[2:], " "),
		Fingerprint: Fingerprint(blob),
	}, nil
}

// looksLikeOptions reports whether the first field of a line is an options
// list rather than a key type: options are comma-separated, either flags
// (no-pty, restrict, cert-authority) or name="value".
func looksLikeOptions(f string) bool {
	return strings.ContainsAny(f, `=,"`) || strings.HasPrefix(f, "no-") ||
		f == "restrict" || f == "cert-authority" || f == "pty" || f == "verify-required" || f == "touch-required"
}

func optionName(f string) string {
	name, _, _ := strings.Cut(f, "=")
	name, _, _ = strings.Cut(name, ",")
	if len(name) > 32 {
		name = name[:32]
	}
	return name
}

// wireReader reads the SSH wire format (RFC 4251): strings and mpints are a
// big-endian uint32 length and that many bytes.
type wireReader struct{ b []byte }

func (r *wireReader) str() ([]byte, error) {
	if len(r.b) < 4 {
		return nil, errors.New("truncated")
	}
	n := binary.BigEndian.Uint32(r.b)
	if uint64(n) > uint64(len(r.b)-4) {
		return nil, errors.New("truncated")
	}
	s := r.b[4 : 4+n]
	r.b = r.b[4+n:]
	return s, nil
}

func checkEd25519(sk bool) func(*wireReader) error {
	return func(r *wireReader) error {
		k, err := r.str()
		if err != nil {
			return err
		}
		if len(k) != 32 {
			return fmt.Errorf("public key is %d bytes, want 32", len(k))
		}
		if sk {
			return checkApplication(r)
		}
		return nil
	}
}

func checkECDSA(curve string, c ecdh.Curve, sk bool) func(*wireReader) error {
	return func(r *wireReader) error {
		name, err := r.str()
		if err != nil {
			return err
		}
		if string(name) != curve {
			return fmt.Errorf("curve %q, want %s", name, curve)
		}
		point, err := r.str()
		if err != nil {
			return err
		}
		// NewPublicKey accepts only an uncompressed point on the curve.
		if _, err := c.NewPublicKey(point); err != nil {
			return errors.New("the public point is not on the curve")
		}
		if sk {
			return checkApplication(r)
		}
		return nil
	}
}

// checkApplication reads a security key's application string, which
// OpenSSH requires to start with "ssh:".
func checkApplication(r *wireReader) error {
	app, err := r.str()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(app), "ssh:") {
		return errors.New("the security key's application does not start with ssh:")
	}
	return nil
}

func checkRSA(r *wireReader) error {
	e, err := r.str()
	if err != nil {
		return err
	}
	n, err := r.str()
	if err != nil {
		return err
	}
	for _, m := range [][]byte{e, n} {
		if len(m) == 0 || m[0]&0x80 != 0 {
			return errors.New("a negative or empty integer")
		}
	}
	ev := new(big.Int).SetBytes(e)
	if ev.Cmp(big.NewInt(3)) < 0 || ev.Bit(0) == 0 || ev.BitLen() > 64 {
		return errors.New("the public exponent is not an odd number of at least 3")
	}
	if bits := new(big.Int).SetBytes(n).BitLen(); bits < minRSABits || bits > 16384 {
		return fmt.Errorf("the modulus is %d bits; at least %d (and at most 16384) are needed", bits, minRSABits)
	}
	return nil
}
