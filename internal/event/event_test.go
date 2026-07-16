package event

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReleasePublishedGolden(t *testing.T) {
	previousRelease := "rel_previous"
	previousCommit := "commit-previous"
	note := "Publish checkout update"
	value, err := NewReleasePublished("evt_0123456789abcdef", time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC), ReleasePublishedData{
		Workspace:         "default",
		AppKey:            "checkout",
		ReleaseID:         "rel_current",
		Commit:            "commit-current",
		PreviousReleaseID: &previousRelease,
		PreviousCommit:    &previousCommit,
		Actor:             "operator@example.test",
		Note:              &note,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	want, err := os.ReadFile(filepath.Join("testdata", "release_published.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("release event mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	for _, protected := range [][]byte{[]byte("credential"), []byte("signing_secret"), []byte("external_key"), []byte("input")} {
		if bytes.Contains(bytes.ToLower(got), protected) {
			t.Fatalf("release event contains protected field %q: %s", protected, got)
		}
	}
}

func TestValidateRejectsUnknownTypeAndFields(t *testing.T) {
	base := Envelope{
		SpecVersion:     CloudEventsSpecVersion,
		ID:              "evt_test",
		Type:            "windforce.unknown",
		Source:          "/workspaces/default/control-plane",
		Subject:         "unknown",
		Time:            time.Now().UTC(),
		DataContentType: JSONContentType,
		Data:            json.RawMessage(`{}`),
	}
	if err := Validate(base); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("unknown type error = %v", err)
	}
	base.Type = ReleasePublishedType
	base.Subject = "apps/echo/releases/release-a"
	base.Data = json.RawMessage(`{"workspace":"default","app_key":"echo","release_id":"release-a","commit":"commit-a","actor":"system","password":"secret"}`)
	if err := Validate(base); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("unknown field error = %v", err)
	}
}
