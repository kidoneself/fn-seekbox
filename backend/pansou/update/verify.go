package update

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func versionParts(v string) ([3]uint64, error) {
	var out [3]uint64
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, errors.New("version must have three numeric components")
	}
	for i, p := range parts {
		if p == "" || len(p) > 9 || (len(p) > 1 && p[0] == '0') {
			return out, errors.New("invalid version")
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return out, errors.New("invalid version")
			}
		}
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return out, err
		}
		out[i] = n
	}
	return out, nil
}
func newer(a, b string) bool {
	av, ae := versionParts(a)
	bv, be := versionParts(b)
	if ae != nil || be != nil {
		return false
	}
	for i := range av {
		if av[i] != bv[i] {
			return av[i] > bv[i]
		}
	}
	return false
}
func verifyManifest(s SignedManifest, c Config) (Manifest, error) {
	var m Manifest
	key, ok := c.PublicKeys[s.KeyID]
	if !ok || len(key) != ed25519.PublicKeySize {
		return m, errors.New("untrusted signing key")
	}
	data, err := base64.StdEncoding.DecodeString(s.Payload)
	if err != nil || len(data) > 64<<10 {
		return m, errors.New("invalid signed payload")
	}
	sig, err := base64.StdEncoding.DecodeString(s.Signature)
	if err != nil || !ed25519.Verify(key, data, sig) {
		return m, errors.New("release signature verification failed")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return m, fmt.Errorf("invalid manifest: %w", err)
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return m, errors.New("trailing manifest data")
	}
	if m.Schema != 1 || m.App != c.App || m.Channel != c.Channel || m.Arch != c.Arch {
		return m, errors.New("release targets a different application/channel/architecture")
	}
	if m.DataSchema != DataSchema || m.MinBootstrap < 1 || m.MinBootstrap > BootstrapVersion {
		return m, errors.New("release requires a full application package upgrade")
	}
	if !newer(m.Version, c.CurrentVersion) {
		return m, errors.New("release version is not newer")
	}
	hash, err := hex.DecodeString(m.SHA256)
	if err != nil || len(hash) != 32 || m.SHA256 != strings.ToLower(m.SHA256) {
		return m, errors.New("invalid package SHA256")
	}
	if m.Size <= 0 || m.Size > MaxArchiveSize {
		return m, errors.New("package exceeds size limit")
	}
	return m, nil
}
func checkedURL(raw string, allowLocal bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid update URL")
	}
	if u.Scheme == "https" {
		return u, nil
	}
	ip := net.ParseIP(u.Hostname())
	if allowLocal && u.Scheme == "http" && ip != nil && ip.IsLoopback() {
		return u, nil
	}
	return nil, errors.New("update source must use HTTPS")
}
func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}
