package core

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/r-thirumaran/mac-utility/shared/go/appdirs"
	"github.com/r-thirumaran/mac-utility/shared/go/securechan"
)

// AppName is the directory name under the per-OS config root.
const AppName = "Edgehop"

// IdentityFile holds the long-term private key inside the config dir.
const IdentityFile = "identity.json"

// identityFile is the on-disk form of the key.
type identityFile struct {
	Version    int    `json:"version"`
	PrivateKey string `json:"privateKey"` // base64 X25519 private key
}

// ConfigDir returns the config directory (docs/ARCHITECTURE.md §8), or dir
// when it is non-empty (the --config-dir flag).
func ConfigDir(dir string) (string, error) {
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("edgehop: config dir: %w", err)
		}
		return dir, nil
	}
	return appdirs.ConfigDir(AppName)
}

// LoadIdentity reads the X25519 identity from <dir>/identity.json, creating
// a fresh one (file mode 0600) when it does not exist.
func LoadIdentity(dir string) (securechan.Identity, error) {
	path := filepath.Join(dir, IdentityFile)
	var f identityFile
	err := appdirs.LoadJSON(path, &f)
	switch {
	case err == nil:
		raw, dErr := base64.StdEncoding.DecodeString(f.PrivateKey)
		if dErr != nil || len(raw) != 32 {
			return securechan.Identity{}, fmt.Errorf("edgehop: %s: bad private key", path)
		}
		var priv [32]byte
		copy(priv[:], raw)
		return securechan.IdentityFromPriv(priv), nil
	case errors.Is(err, fs.ErrNotExist):
		id, gErr := securechan.NewIdentity()
		if gErr != nil {
			return securechan.Identity{}, gErr
		}
		f = identityFile{Version: 1, PrivateKey: base64.StdEncoding.EncodeToString(id.Priv[:])}
		if sErr := appdirs.SaveJSON(path, f); sErr != nil {
			return securechan.Identity{}, sErr
		}
		return id, nil
	default:
		return securechan.Identity{}, err
	}
}

// Platform returns the protocol platform string for this build
// ("macos", "windows" or "linux").
func Platform() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	}
	return "linux"
}

// DefaultMachineName is the hostname without a domain suffix, "This
// computer" when unknown.
func DefaultMachineName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "This computer"
	}
	if i := strings.IndexByte(h, '.'); i > 0 {
		h = h[:i]
	}
	return h
}

// PubKeyB64 is the base64 form of a static public key as carried in the
// Bonjour TXT record and the config file.
func PubKeyB64(pub [32]byte) string { return base64.StdEncoding.EncodeToString(pub[:]) }

// ParsePubKey decodes a base64 static public key.
func ParsePubKey(s string) ([32]byte, error) {
	var k [32]byte
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return k, fmt.Errorf("edgehop: public key: %w", err)
	}
	if len(raw) != 32 {
		return k, fmt.Errorf("edgehop: public key is %d bytes", len(raw))
	}
	copy(k[:], raw)
	return k, nil
}
