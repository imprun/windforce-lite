package event

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
)

const (
	CloudEventsSpecVersion = "1.0"
	JSONContentType        = "application/json"
	ReleasePublishedType   = "windforce.release.published"
)

var ErrInvalidEvent = errors.New("invalid control plane event")

type Envelope struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Type            string          `json:"type"`
	Source          string          `json:"source"`
	Subject         string          `json:"subject"`
	Time            time.Time       `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	Data            json.RawMessage `json:"data"`
}

type ReleasePublishedData struct {
	Workspace         string  `json:"workspace"`
	AppKey            string  `json:"app_key"`
	ReleaseID         string  `json:"release_id"`
	Commit            string  `json:"commit"`
	PreviousReleaseID *string `json:"previous_release_id,omitempty"`
	PreviousCommit    *string `json:"previous_commit,omitempty"`
	Actor             string  `json:"actor"`
	Note              *string `json:"note,omitempty"`
}

func NewReleasePublished(id string, occurredAt time.Time, data ReleasePublishedData) (Envelope, error) {
	data.Workspace = contract.NormalizeWorkspace(data.Workspace)
	data.Actor = strings.TrimSpace(data.Actor)
	if data.Actor == "" {
		data.Actor = "system"
	}
	if data.Note != nil {
		note := strings.TrimSpace(*data.Note)
		if note == "" {
			data.Note = nil
		} else {
			data.Note = &note
		}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return Envelope{}, err
	}
	event := Envelope{
		SpecVersion:     CloudEventsSpecVersion,
		ID:              strings.TrimSpace(id),
		Type:            ReleasePublishedType,
		Source:          "/workspaces/" + data.Workspace + "/control-plane",
		Subject:         "apps/" + data.AppKey + "/releases/" + data.ReleaseID,
		Time:            occurredAt.UTC(),
		DataContentType: JSONContentType,
		Data:            raw,
	}
	if err := Validate(event); err != nil {
		return Envelope{}, err
	}
	return event, nil
}

func Validate(value Envelope) error {
	if value.SpecVersion != CloudEventsSpecVersion {
		return invalid("specversion must be %q", CloudEventsSpecVersion)
	}
	if !strings.HasPrefix(value.ID, "evt_") || len(value.ID) <= len("evt_") {
		return invalid("id must use the evt_ prefix")
	}
	if value.Time.IsZero() {
		return invalid("time is required")
	}
	if value.DataContentType != JSONContentType {
		return invalid("datacontenttype must be %q", JSONContentType)
	}
	if !json.Valid(value.Data) {
		return invalid("data must be valid JSON")
	}
	switch value.Type {
	case ReleasePublishedType:
		return validateReleasePublished(value)
	default:
		return invalid("unsupported event type %q", value.Type)
	}
}

func ReleasePublished(value Envelope) (ReleasePublishedData, error) {
	if value.Type != ReleasePublishedType {
		return ReleasePublishedData{}, invalid("event type is %q", value.Type)
	}
	var data ReleasePublishedData
	decoder := json.NewDecoder(bytes.NewReader(value.Data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return ReleasePublishedData{}, invalid("release data: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ReleasePublishedData{}, invalid("release data has trailing values")
	}
	return data, nil
}

func validateReleasePublished(value Envelope) error {
	data, err := ReleasePublished(value)
	if err != nil {
		return err
	}
	data.Workspace = contract.NormalizeWorkspace(data.Workspace)
	if strings.TrimSpace(data.AppKey) == "" {
		return invalid("data.app_key is required")
	}
	if strings.TrimSpace(data.ReleaseID) == "" {
		return invalid("data.release_id is required")
	}
	if strings.TrimSpace(data.Commit) == "" {
		return invalid("data.commit is required")
	}
	if strings.TrimSpace(data.Actor) == "" {
		return invalid("data.actor is required")
	}
	wantSource := "/workspaces/" + data.Workspace + "/control-plane"
	if value.Source != wantSource {
		return invalid("source must be %q", wantSource)
	}
	wantSubject := "apps/" + data.AppKey + "/releases/" + data.ReleaseID
	if value.Subject != wantSubject {
		return invalid("subject must be %q", wantSubject)
	}
	return nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidEvent, fmt.Sprintf(format, args...))
}
