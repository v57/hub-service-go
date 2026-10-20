package hubservice

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain keeps the device key of every test in a temp dir, never in
// ~/.hub.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "hub-service-go-device-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HUB_DEVICE_KEY", filepath.Join(dir, "device.key"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
