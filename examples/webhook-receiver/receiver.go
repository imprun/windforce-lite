package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	webhookcontract "github.com/imprun/windforce-lite/pkg/webhook"
)

const maxWebhookBodyBytes = 1 << 20

type receiver struct {
	verifier webhookcontract.Verifier
	logger   *slog.Logger

	mu                sync.Mutex
	seen              map[string]struct{}
	events            []acceptedEvent
	failuresRemaining int
}

type cloudEvent struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Type            string          `json:"type"`
	Source          string          `json:"source"`
	Subject         string          `json:"subject"`
	Time            time.Time       `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	Data            json.RawMessage `json:"data"`
}

type releasePublishedData struct {
	Workspace         string  `json:"workspace"`
	AppKey            string  `json:"app_key"`
	ReleaseID         string  `json:"release_id"`
	Commit            string  `json:"commit"`
	PreviousReleaseID *string `json:"previous_release_id,omitempty"`
	PreviousCommit    *string `json:"previous_commit,omitempty"`
	Actor             string  `json:"actor"`
	Note              *string `json:"note,omitempty"`
}

type webhookTestData struct {
	Workspace      string `json:"workspace"`
	SubscriptionID string `json:"subscription_id"`
	Actor          string `json:"actor"`
}

type acceptedEvent struct {
	EventID    string    `json:"event_id"`
	EventType  string    `json:"event_type"`
	Subject    string    `json:"subject"`
	AppKey     string    `json:"app_key,omitempty"`
	ReleaseID  string    `json:"release_id,omitempty"`
	Commit     string    `json:"commit,omitempty"`
	Actor      string    `json:"actor"`
	Note       *string   `json:"note,omitempty"`
	AcceptedAt time.Time `json:"accepted_at"`
}

func newReceiver(secret string, timestampTolerance time.Duration, failFirst int, logger *slog.Logger) *receiver {
	if logger == nil {
		logger = slog.Default()
	}
	return &receiver{
		verifier:          webhookcontract.Verifier{Secret: secret, TimestampTolerance: timestampTolerance},
		logger:            logger,
		seen:              map[string]struct{}{},
		failuresRemaining: failFirst,
	}
}

func (receiver *receiver) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/healthz":
		response.WriteHeader(http.StatusOK)
	case request.Method == http.MethodGet && request.URL.Path == "/events":
		events := receiver.eventsSnapshot()
		writeReceiverJSON(response, http.StatusOK, map[string]any{"count": len(events), "events": events})
	case request.Method == http.MethodPost && request.URL.Path == "/webhook":
		receiver.receive(response, request)
	default:
		http.NotFound(response, request)
	}
}

func (receiver *receiver) receive(response http.ResponseWriter, request *http.Request) {
	contentType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0])
	if contentType != "application/cloudevents+json" {
		http.Error(response, "Content-Type must be application/cloudevents+json", http.StatusUnsupportedMediaType)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, maxWebhookBodyBytes)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		http.Error(response, "request body is too large or unreadable", http.StatusBadRequest)
		return
	}
	verification, err := receiver.verifier.Verify(request.Header, body)
	if err != nil {
		http.Error(response, "webhook verification failed", http.StatusUnauthorized)
		return
	}
	event, summary, err := decodeAcceptedEvent(body)
	if err != nil {
		http.Error(response, "unsupported webhook event", http.StatusUnprocessableEntity)
		return
	}
	if event.ID != verification.EventID || event.Type != verification.EventType {
		http.Error(response, "webhook identity headers do not match the body", http.StatusBadRequest)
		return
	}

	receiver.mu.Lock()
	if receiver.failuresRemaining > 0 {
		receiver.failuresRemaining--
		receiver.mu.Unlock()
		http.Error(response, "simulated receiver failure", http.StatusServiceUnavailable)
		return
	}
	if _, duplicate := receiver.seen[event.ID]; duplicate {
		receiver.mu.Unlock()
		response.Header().Set("X-Windforce-Duplicate", "true")
		writeReceiverJSON(response, http.StatusOK, map[string]any{"accepted": true, "duplicate": true})
		return
	}
	receiver.seen[event.ID] = struct{}{}
	summary.AcceptedAt = time.Now().UTC()
	receiver.events = append(receiver.events, summary)
	receiver.mu.Unlock()

	receiver.logger.Info("windforce webhook accepted", "event_id", event.ID, "event_type", event.Type, "subject", event.Subject)
	writeReceiverJSON(response, http.StatusAccepted, map[string]any{"accepted": true, "duplicate": false})
}

func decodeAcceptedEvent(body []byte) (cloudEvent, acceptedEvent, error) {
	var event cloudEvent
	if err := decodeStrictJSON(body, &event); err != nil {
		return cloudEvent{}, acceptedEvent{}, err
	}
	if event.SpecVersion != "1.0" || !strings.HasPrefix(event.ID, "evt_") || event.Source == "" || event.Subject == "" || event.Time.IsZero() || event.DataContentType != "application/json" {
		return cloudEvent{}, acceptedEvent{}, errors.New("invalid CloudEvents envelope")
	}
	summary := acceptedEvent{EventID: event.ID, EventType: event.Type, Subject: event.Subject}
	switch event.Type {
	case "windforce.release.published":
		var data releasePublishedData
		if err := decodeStrictJSON(event.Data, &data); err != nil {
			return cloudEvent{}, acceptedEvent{}, err
		}
		if data.Workspace == "" || data.AppKey == "" || data.ReleaseID == "" || data.Commit == "" || data.Actor == "" {
			return cloudEvent{}, acceptedEvent{}, errors.New("invalid release data")
		}
		if event.Source != "/workspaces/"+data.Workspace+"/control-plane" || event.Subject != "apps/"+data.AppKey+"/releases/"+data.ReleaseID {
			return cloudEvent{}, acceptedEvent{}, errors.New("release source or subject does not match data")
		}
		summary.AppKey = data.AppKey
		summary.ReleaseID = data.ReleaseID
		summary.Commit = data.Commit
		summary.Actor = data.Actor
		summary.Note = data.Note
	case "windforce.webhook.test":
		var data webhookTestData
		if err := decodeStrictJSON(event.Data, &data); err != nil {
			return cloudEvent{}, acceptedEvent{}, err
		}
		if data.Workspace == "" || data.SubscriptionID == "" || data.Actor == "" {
			return cloudEvent{}, acceptedEvent{}, errors.New("invalid webhook test data")
		}
		if event.Source != "/workspaces/"+data.Workspace+"/control-plane" || event.Subject != "webhooks/"+data.SubscriptionID+"/test" {
			return cloudEvent{}, acceptedEvent{}, errors.New("test source or subject does not match data")
		}
		summary.Actor = data.Actor
	default:
		return cloudEvent{}, acceptedEvent{}, errors.New("unsupported event type")
	}
	return event, summary, nil
}

func decodeStrictJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("JSON has trailing values")
	}
	return nil
}

func (receiver *receiver) eventsSnapshot() []acceptedEvent {
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	return append([]acceptedEvent(nil), receiver.events...)
}

func writeReceiverJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
