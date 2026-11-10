package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FromEnv reads only local administrator-provided trust configuration. A server
// response cannot install a new verification key or change the trusted origin.
func FromEnv(version string) (Config, error) {
	c := Config{Root: os.Getenv("SEEKBOX_RUNTIME_ROOT"), CurrentVersion: version, BaseURL: os.Getenv("SEEKBOX_UPDATE_BASE_URL"), HelperPath: os.Getenv("SEEKBOX_UPDATE_HELPER"), BootstrapCmd: os.Getenv("SEEKBOX_BOOTSTRAP_CMD"), HealthURL: os.Getenv("SEEKBOX_UPDATE_HEALTH_URL"), HealthSocket: os.Getenv("GATEWAY_SOCKET"), Channel: os.Getenv("SEEKBOX_UPDATE_CHANNEL"), PublicKeys: map[string]ed25519.PublicKey{}, AllowLoopbackHTTP: os.Getenv("SEEKBOX_UPDATE_ALLOW_LOOPBACK_HTTP") == "1"}
	if c.Root == "" {
		c.Root = filepath.Join(".", "runtime")
	}
	if c.HealthURL == "" {
		c.HealthURL = "http://localhost/app/fn-seekbox/api/health"
	}
	raw := []byte(os.Getenv("SEEKBOX_UPDATE_PUBLIC_KEYS"))
	if file := os.Getenv("SEEKBOX_UPDATE_PUBLIC_KEYS_FILE"); file != "" {
		var err error
		raw, err = os.ReadFile(file)
		if err != nil {
			return c, fmt.Errorf("cannot read trusted update key file: %w", err)
		}
	}
	if len(raw) > 0 {
		var keys map[string]string
		if err := json.Unmarshal(raw, &keys); err != nil {
			return c, fmt.Errorf("invalid update trust configuration: %w", err)
		}
		for id, value := range keys {
			key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
			if err != nil || len(key) != ed25519.PublicKeySize || id == "" {
				return c, fmt.Errorf("invalid update public key %q", id)
			}
			c.PublicKeys[id] = ed25519.PublicKey(key)
		}
	}
	return c, nil
}
