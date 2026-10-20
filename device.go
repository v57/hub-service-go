package hubservice

// Device key and service certificates (Docs/Devices/protocol.md section 3).
// A device is one OS instance with its own Ed25519 key. It certifies the
// service keys that run on it; the certificate goes in the `device` header
// next to `auth`, and a hub that approved the device attaches the connection
// to it. The key file uses the keychain layout (base64 of PKCS8 DER private
// key + SPKI DER public key) at, in order:
//
//   - $HUB_DEVICE_KEY
//   - /var/lib/hub/device.key for system services (uid 0)
//   - ~/.hub/device.key for the user
//
// The first SDK that needs it creates it (directory 0700, file 0600).

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	systemDeviceKeyPath = "/var/lib/hub/device.key"
	userDeviceKeyPath   = ".hub/device.key"
	// CertificateLifetime is the default and longest certificate lifetime.
	CertificateLifetime = 7 * 24 * time.Hour
	// certificateRenewal renews a certificate with less than this left.
	certificateRenewal = 24 * time.Hour
	maxInstanceBytes   = 128
	maxNameBytes       = 64
)

// DeviceKey is a device's Ed25519 key.
type DeviceKey struct {
	private   ed25519.PrivateKey
	publicDer []byte
}

var deviceKeychain struct {
	sync.Mutex
	path string
	key  *DeviceKey
}

// SetDeviceKeyPath overrides the device key file location.
func SetDeviceKeyPath(path string) {
	deviceKeychain.Lock()
	defer deviceKeychain.Unlock()
	deviceKeychain.path = path
	deviceKeychain.key = nil
}

// DeviceKeyPath returns the device key file location.
func DeviceKeyPath() (string, error) {
	deviceKeychain.Lock()
	defer deviceKeychain.Unlock()
	return deviceKeyPathLocked()
}

func deviceKeyPathLocked() (string, error) {
	if deviceKeychain.path != "" {
		return deviceKeychain.path, nil
	}
	if env := os.Getenv("HUB_DEVICE_KEY"); env != "" {
		return env, nil
	}
	if os.Geteuid() == 0 {
		return systemDeviceKeyPath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("device key: %w", err)
	}
	return filepath.Join(home, userDeviceKeyPath), nil
}

// LoadDeviceKey returns this device's key, creating the key file on first use.
func LoadDeviceKey() (*DeviceKey, error) {
	deviceKeychain.Lock()
	defer deviceKeychain.Unlock()
	if deviceKeychain.key != nil {
		return deviceKeychain.key, nil
	}
	path, err := deviceKeyPathLocked()
	if err != nil {
		return nil, err
	}
	key, err := LoadDeviceKeyFile(path)
	if err != nil {
		return nil, err
	}
	deviceKeychain.key = key
	return key, nil
}

// LoadDeviceKeyFile reads a device key file, creating it if absent.
func LoadDeviceKeyFile(path string) (*DeviceKey, error) {
	text, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := createDeviceKey(path); err != nil {
			return nil, fmt.Errorf("device key: %w", err)
		}
		text, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("device key: %w", err)
	}
	key, err := parseKey(strings.TrimSpace(string(text)))
	if err != nil {
		return nil, fmt.Errorf("invalid device key file %q: %w", path, err)
	}
	return &DeviceKey{private: key.private, publicDer: key.publicDer}, nil
}

// createDeviceKey writes a new key to a temp file and links it into place, so
// readers never see a partial file and of two processes creating it at once
// both end up with the first one's key.
func createDeviceKey(path string) error {
	encoded, err := generateKey()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(encoded); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return nil
}

// NewDeviceKey wraps an Ed25519 private key, for embedders that keep the
// device key elsewhere and for tests.
func NewDeviceKey(private ed25519.PrivateKey) (*DeviceKey, error) {
	der, err := x509.MarshalPKIXPublicKey(private.Public())
	if err != nil {
		return nil, err
	}
	return &DeviceKey{private: private, publicDer: der}, nil
}

// PublicKey is the device's SPKI public key, base64.
func (k *DeviceKey) PublicKey() string {
	return base64.StdEncoding.EncodeToString(k.publicDer)
}

// ID is the device id: the first 32 hex characters of SHA-256 of its SPKI.
func (k *DeviceKey) ID() string {
	sum := sha256.Sum256(k.publicDer)
	return hex.EncodeToString(sum[:])[:32]
}

// SafetyCode is the "ddd ddd" code shown on the device and the approving
// device so an owner can check they approve the right one.
func (k *DeviceKey) SafetyCode() string {
	sum := sha256.Sum256(k.publicDer)
	n, _ := strconv.ParseUint(hex.EncodeToString(sum[:4]), 16, 64)
	code := fmt.Sprintf("%06d", n%1000000)
	return code[:3] + " " + code[3:]
}

// Sign signs data with the device key (standard base64), as used for
// `ssh1:<hostKey>` host key signatures.
func (k *DeviceKey) Sign(data string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(k.private, []byte(data)))
}

// Certificate is a device's statement that a service key runs on it.
type Certificate struct {
	V          int    `json:"v"`
	Device     string `json:"device"`
	Service    string `json:"service"`
	Instance   string `json:"instance"`
	DeviceName string `json:"deviceName,omitempty"`
	Platform   string `json:"platform,omitempty"`
	Parent     string `json:"parent,omitempty"`
	Hub        string `json:"hub,omitempty"`
	Iat        int64  `json:"iat"`
	Exp        int64  `json:"exp"`
}

// Certify signs a certificate, filling V and Device, and returns the
// `device` header value `<base64url(JSON)>.<signature>`.
func (k *DeviceKey) Certify(certificate Certificate) (string, error) {
	certificate.V = 1
	certificate.Device = k.PublicKey()
	if err := certificate.validate(); err != nil {
		return "", err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(certificate); err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(bytes.TrimSuffix(buffer.Bytes(), []byte("\n")))
	return payload + "." + k.Sign(payload), nil
}

func (c Certificate) validate() error {
	if c.Service == "" {
		return errors.New("certificate: service key required")
	}
	if c.Instance == "" || len(c.Instance) > maxInstanceBytes || !utf8.ValidString(c.Instance) {
		return fmt.Errorf("certificate: instance must be 1-%d UTF-8 bytes", maxInstanceBytes)
	}
	if len(c.DeviceName) > maxNameBytes || len(c.Parent) > maxNameBytes {
		return fmt.Errorf("certificate: deviceName and parent are at most %d bytes", maxNameBytes)
	}
	if c.Exp <= c.Iat || c.Exp-c.Iat > int64(CertificateLifetime/time.Second) {
		return errors.New("certificate: lifetime must be at most 7 days")
	}
	return nil
}

// VerifyCertificate checks a `device` header's signature and returns its
// certificate. It doesn't check times or the service key.
func VerifyCertificate(header string) (Certificate, error) {
	payload, signature, ok := strings.Cut(header, ".")
	if !ok {
		return Certificate{}, errors.New("certificate: malformed")
	}
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Certificate{}, fmt.Errorf("certificate: %w", err)
	}
	var certificate Certificate
	if err := json.Unmarshal(data, &certificate); err != nil {
		return Certificate{}, fmt.Errorf("certificate: %w", err)
	}
	device, err := parseSPKI(certificate.Device)
	if err != nil {
		return Certificate{}, fmt.Errorf("certificate: device key: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || !ed25519.Verify(device, []byte(payload), raw) {
		return Certificate{}, errors.New("certificate: invalid signature")
	}
	return certificate, nil
}

// DeviceOptions configures device mode: the `device` header that certifies
// the service key with this device's key.
type DeviceOptions struct {
	// Disabled sends no `device` header; the service is a plain key.
	Disabled bool
	// Key is the device key. Default: LoadDeviceKey.
	Key *DeviceKey
	// Instance names this service on its device, unique per device
	// (`s3-local/Backup Disk`). Default: the service Name, else the working
	// directory's name, else "service".
	Instance string
	// Name is the suggested device name. Default: the computer name on
	// macOS, else the host name without `.local`.
	Name string
	// Platform defaults to the OS (`macos`, `linux`, ...).
	Platform string
	// Parent is the declared parent device name for containers and VMs.
	Parent string
	// Lifetime of each certificate, at most 7 days (the default).
	Lifetime time.Duration
}

// deviceCertifier renews the connection's certificate when it is missing,
// for another service key, or has less than a day left.
type deviceCertifier struct {
	options DeviceOptions
	mu      sync.Mutex
	header  string
	service string
	expires time.Time
	now     func() time.Time
}

func newDeviceCertifier(options DeviceOptions, serviceName string) *deviceCertifier {
	if options.Instance == "" {
		options.Instance = serviceName
	}
	if options.Instance == "" {
		if dir, err := os.Getwd(); err == nil && filepath.Base(dir) != "/" && filepath.Base(dir) != "." {
			options.Instance = filepath.Base(dir)
		}
	}
	if options.Instance == "" {
		options.Instance = "service"
	}
	if options.Name == "" {
		options.Name = hostDeviceName()
	}
	if options.Platform == "" {
		options.Platform = hostPlatform()
	}
	if options.Lifetime <= 0 || options.Lifetime > CertificateLifetime {
		options.Lifetime = CertificateLifetime
	}
	options.Instance = truncateUTF8(options.Instance, maxInstanceBytes)
	options.Name = truncateUTF8(options.Name, maxNameBytes)
	options.Parent = truncateUTF8(options.Parent, maxNameBytes)
	return &deviceCertifier{options: options, now: time.Now}
}

// header returns the `device` header for serviceKey (SPKI).
func (d *deviceCertifier) certificate(serviceKey string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	if d.header != "" && d.service == serviceKey && d.expires.Sub(now) >= certificateRenewal {
		return d.header, nil
	}
	key := d.options.Key
	if key == nil {
		loaded, err := LoadDeviceKey()
		if err != nil {
			return "", err
		}
		key = loaded
	}
	expires := now.Add(d.options.Lifetime)
	header, err := key.Certify(Certificate{
		Service:    serviceKey,
		Instance:   d.options.Instance,
		DeviceName: d.options.Name,
		Platform:   d.options.Platform,
		Parent:     d.options.Parent,
		Iat:        now.Unix(),
		Exp:        expires.Unix(),
	})
	if err != nil {
		return "", err
	}
	d.header, d.service, d.expires = header, serviceKey, time.Unix(expires.Unix(), 0)
	return header, nil
}

func hostDeviceName() string {
	if runtime.GOOS == "darwin" {
		if output, err := exec.Command("scutil", "--get", "ComputerName").Output(); err == nil {
			if name := strings.TrimSpace(string(output)); name != "" {
				return name
			}
		}
	}
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(name, ".local")
}

func hostPlatform() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "linux", "windows", "android", "ios":
		return runtime.GOOS
	default:
		return "other"
	}
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
