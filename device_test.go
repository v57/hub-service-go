package hubservice

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func vectorDeviceKey(t *testing.T, v vectors) *DeviceKey {
	t.Helper()
	private, err := KeyFromFile(v.Keys["device"].KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	key, err := NewDeviceKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestDeviceKeyMatchesVectors(t *testing.T) {
	v := loadVectors(t)
	key := vectorDeviceKey(t, v)
	device := v.Keys["device"]
	if key.PublicKey() != device.PublicKey || key.ID() != device.DeviceID || key.SafetyCode() != device.SafetyCode {
		t.Fatalf("device key: %s %s %q", key.PublicKey(), key.ID(), key.SafetyCode())
	}
}

func TestCertificatesMatchVectors(t *testing.T) {
	v := loadVectors(t)
	key := vectorDeviceKey(t, v)
	service := v.Keys["service"].PublicKey
	header, err := key.Certify(Certificate{Service: service, Instance: "s3-local/Backup Disk", DeviceName: "Mac Studio",
		Platform: "macos", Iat: 1792500000, Exp: 1793104800})
	if err != nil || header != v.Certificate.Header {
		t.Fatalf("certificate:\n got %s\nwant %s (%v)", header, v.Certificate.Header, err)
	}
	payload, _, _ := strings.Cut(header, ".")
	if json, _ := base64.RawURLEncoding.DecodeString(payload); string(json) != v.Certificate.JSON {
		t.Fatalf("certificate JSON %s", json)
	}
	child, err := key.Certify(Certificate{Service: service, Instance: "builder", DeviceName: "builder",
		Platform: "linux", Parent: "Mac Studio", Iat: 1792500000, Exp: 1793104800})
	if err != nil || child != v.ChildCertificate.Header {
		t.Fatalf("child certificate:\n got %s\nwant %s (%v)", child, v.ChildCertificate.Header, err)
	}
	certificate, err := VerifyCertificate(child)
	if err != nil || certificate.Parent != "Mac Studio" || certificate.Device != key.PublicKey() || certificate.Service != service {
		t.Fatalf("verify: %+v %v", certificate, err)
	}
	if _, err := VerifyCertificate(strings.Replace(child, "Q", "R", 1)); err == nil {
		t.Fatal("tampered certificate verified")
	}
}

func TestCertifyRejectsInvalidCertificates(t *testing.T) {
	key := vectorDeviceKey(t, loadVectors(t))
	for name, certificate := range map[string]Certificate{
		"no service":    {Instance: "a", Iat: 1, Exp: 2},
		"no instance":   {Service: "s", Iat: 1, Exp: 2},
		"long instance": {Service: "s", Instance: strings.Repeat("a", 129), Iat: 1, Exp: 2},
		"long name":     {Service: "s", Instance: "a", DeviceName: strings.Repeat("a", 65), Iat: 1, Exp: 2},
		"eight days":    {Service: "s", Instance: "a", Iat: 1, Exp: 1 + 8*86400},
		"expired first": {Service: "s", Instance: "a", Iat: 2, Exp: 1},
	} {
		if _, err := key.Certify(certificate); err == nil {
			t.Errorf("%s: certified", name)
		}
	}
}

func TestDeviceKeyFileCreatedOnceWithModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub", "device.key")
	var wg sync.WaitGroup
	keys := make([]string, 8)
	for i := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, err := LoadDeviceKeyFile(path)
			if err != nil {
				t.Error(err)
				return
			}
			keys[i] = key.PublicKey()
		}()
	}
	wg.Wait()
	for _, key := range keys {
		if key != keys[0] {
			t.Fatalf("racing creators got different keys: %v", keys)
		}
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("device.key mode %v", info.Mode().Perm())
	}
	if info, _ := os.Stat(filepath.Dir(path)); info.Mode().Perm() != 0o700 {
		t.Fatalf("device key dir mode %v", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp files left: %v", entries)
	}
}

func TestDeviceKeyPathOrder(t *testing.T) {
	SetDeviceKeyPath("")
	t.Cleanup(func() { SetDeviceKeyPath("") })
	t.Setenv("HUB_DEVICE_KEY", "/tmp/x/device.key")
	if path, _ := DeviceKeyPath(); path != "/tmp/x/device.key" {
		t.Fatalf("HUB_DEVICE_KEY: %q", path)
	}
	t.Setenv("HUB_DEVICE_KEY", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".hub", "device.key")
	if os.Geteuid() == 0 {
		want = "/var/lib/hub/device.key"
	}
	if path, _ := DeviceKeyPath(); path != want {
		t.Fatalf("default path %q, want %q", path, want)
	}
	SetDeviceKeyPath("/opt/device.key")
	if path, _ := DeviceKeyPath(); path != "/opt/device.key" {
		t.Fatalf("override: %q", path)
	}
}

func TestCertifierRenewsWithLessThanADayLeft(t *testing.T) {
	v := loadVectors(t)
	certifier := newDeviceCertifier(DeviceOptions{Key: vectorDeviceKey(t, v), Name: "Mac Studio"}, "s3-local/Backup Disk")
	now := time.Unix(1792500000, 0)
	certifier.now = func() time.Time { return now }
	service := v.Keys["service"].PublicKey
	first, err := certifier.certificate(service)
	if err != nil {
		t.Fatal(err)
	}
	if certificate, _ := VerifyCertificate(first); certificate.Exp-certificate.Iat != 7*86400 ||
		certificate.Instance != "s3-local/Backup Disk" || certificate.DeviceName != "Mac Studio" {
		t.Fatalf("certificate: %+v", certificate)
	}
	now = now.Add(5 * 24 * time.Hour)
	if again, _ := certifier.certificate(service); again != first {
		t.Fatal("renewed with two days left")
	}
	now = now.Add(36 * time.Hour)
	renewed, _ := certifier.certificate(service)
	if renewed == first {
		t.Fatal("not renewed with half a day left")
	}
	if other, _ := certifier.certificate(v.Keys["hub"].PublicKey); other == renewed {
		t.Fatal("same certificate for another service key")
	}
}

func TestServiceSendsDeviceHeader(t *testing.T) {
	v := loadVectors(t)
	hub := newInfoHub(t, "")
	private, _ := KeyFromFile(v.Keys["service"].KeyFile)
	device := vectorDeviceKey(t, v)
	s := New(ServiceOptions{Address: hub.port, Key: private, Name: "Backup",
		Device: DeviceOptions{Key: device, Instance: "s3-local/Backup Disk", Parent: "Mac Studio"}})
	s.Start()
	defer s.Stop()
	headers := hub.next(t)
	certificate, err := VerifyCertificate(headers["device"])
	if err != nil {
		t.Fatalf("device header %q: %v", headers["device"], err)
	}
	if certificate.Device != device.PublicKey() || certificate.Service != v.Keys["service"].PublicKey ||
		certificate.Instance != "s3-local/Backup Disk" || certificate.Parent != "Mac Studio" || certificate.Platform == "" {
		t.Fatalf("certificate: %+v", certificate)
	}
	if now := time.Now().Unix(); certificate.Iat > now+1 || certificate.Exp < now+6*86400 {
		t.Fatalf("certificate times: %+v", certificate)
	}

	plain := New(ServiceOptions{Key: private, Device: DeviceOptions{Disabled: true}})
	if _, ok := plain.authHeaders()["device"]; ok {
		t.Fatal("device header with device mode disabled")
	}
	custom := New(ServiceOptions{Sign: noSign})
	if _, ok := custom.authHeaders()["device"]; ok {
		t.Fatal("device header with a custom signer")
	}
	named := New(ServiceOptions{Key: private, Name: "Echo"})
	certificate, err = VerifyCertificate(named.authHeaders()["device"])
	if err != nil || certificate.Instance != "Echo" {
		t.Fatalf("default instance: %+v %v", certificate, err)
	}
	if path, _ := DeviceKeyPath(); !strings.HasPrefix(path, os.TempDir()) && !strings.Contains(path, "hub-service-go-device-") {
		t.Fatalf("test device key outside the temp dir: %s", path)
	}
}
