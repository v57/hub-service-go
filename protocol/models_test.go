package protocol

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type rendezvousFixture struct {
	Ticket    RendezvousTicket `json:"ticket"`
	Candidate Candidate        `json:"candidate"`
}

func TestExactJSONRoundTrips(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{"shares-replace.json", &SharesReplaceMessage{}},
		{"grant.json", &ConnectionGrant{}},
		{"rendezvous.json", &rendezvousFixture{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join("testdata", "v1", test.name)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(want, test.value); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			got, err := json.MarshalIndent(test.value, "", "  ")
			if err != nil {
				t.Fatalf("encode fixture: %v", err)
			}
			got = append(got, '\n')
			if !bytes.Equal(got, want) {
				t.Fatalf("fixture changed after round trip\nwant:\n%s\ngot:\n%s", want, got)
			}
		})
	}
}

func TestAllFixturesUseVersionOne(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "v1", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		var value any
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		assertVersions(t, path, value)
	}
}

func assertVersions(t *testing.T, path string, value any) {
	t.Helper()
	switch value := value.(type) {
	case map[string]any:
		if version, exists := value["version"]; exists && version != float64(WireVersion) {
			t.Errorf("%s contains unsupported version %v", path, version)
		}
		for _, child := range value {
			assertVersions(t, path, child)
		}
	case []any:
		for _, child := range value {
			assertVersions(t, path, child)
		}
	}
}
