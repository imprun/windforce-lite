package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	controlevent "github.com/imprun/windforce-lite/internal/event"
	"github.com/imprun/windforce-lite/internal/webhook"
)

type canonicalWebhookSubscription struct {
	ID               string     `json:"id"`
	WorkspaceID      string     `json:"workspace_id"`
	Name             string     `json:"name"`
	EndpointSummary  string     `json:"endpoint_summary"`
	HasSigningSecret bool       `json:"has_signing_secret"`
	EventTypes       []string   `json:"event_types"`
	AppKeys          []string   `json:"app_keys"`
	Enabled          bool       `json:"enabled"`
	CreatedBy        string     `json:"created_by"`
	UpdatedBy        string     `json:"updated_by"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
}

type canonicalWebhookSubscriptionResponse struct {
	Subscription  canonicalWebhookSubscription `json:"subscription"`
	SigningSecret string                       `json:"signing_secret,omitempty"`
}

type canonicalWebhookCreateRequest struct {
	Name          string   `json:"name"`
	Endpoint      string   `json:"endpoint"`
	SigningSecret string   `json:"signing_secret,omitempty"`
	EventTypes    []string `json:"event_types,omitempty"`
	AppKeys       []string `json:"app_keys,omitempty"`
	Enabled       *bool    `json:"enabled,omitempty"`
}

type canonicalWebhookUpdateRequest struct {
	Name                *string   `json:"name,omitempty"`
	Endpoint            *string   `json:"endpoint,omitempty"`
	SigningSecret       *string   `json:"signing_secret,omitempty"`
	RotateSigningSecret bool      `json:"rotate_signing_secret,omitempty"`
	EventTypes          *[]string `json:"event_types,omitempty"`
	AppKeys             *[]string `json:"app_keys,omitempty"`
	Enabled             *bool     `json:"enabled,omitempty"`
}

type canonicalWebhookDeliveryPage struct {
	Items      []webhook.DeliveryDetail `json:"items"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}

type canonicalWebhookDeliveryCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func (h *Handler) handleCanonicalWebhookAPI(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "w" {
		return false
	}
	workspaceID := parts[2]
	switch parts[3] {
	case "webhooks":
		switch {
		case len(parts) == 4 && r.Method == http.MethodGet:
			h.handleCanonicalWebhooks(w, r, workspaceID)
		case len(parts) == 4 && r.Method == http.MethodPost:
			h.handleCanonicalCreateWebhook(w, r, workspaceID)
		case len(parts) == 5 && r.Method == http.MethodGet:
			h.handleCanonicalWebhook(w, r, workspaceID, parts[4])
		case len(parts) == 5 && r.Method == http.MethodPatch:
			h.handleCanonicalUpdateWebhook(w, r, workspaceID, parts[4])
		case len(parts) == 5 && r.Method == http.MethodDelete:
			h.handleCanonicalDeleteWebhook(w, r, workspaceID, parts[4])
		case len(parts) == 6 && parts[5] == "test" && r.Method == http.MethodPost:
			h.handleCanonicalTestWebhook(w, r, workspaceID, parts[4])
		case len(parts) == 6 && parts[5] == "deliveries" && r.Method == http.MethodGet:
			h.handleCanonicalWebhookDeliveries(w, r, workspaceID, parts[4])
		default:
			return false
		}
		return true
	case "webhook-deliveries":
		switch {
		case len(parts) == 5 && r.Method == http.MethodGet:
			h.handleCanonicalWebhookDelivery(w, r, workspaceID, parts[4])
		case len(parts) == 6 && parts[5] == "retry" && r.Method == http.MethodPost:
			h.handleCanonicalRetryWebhookDelivery(w, r, workspaceID, parts[4])
		default:
			return false
		}
		return true
	default:
		return false
	}
}

func (h *Handler) webhookStore(w http.ResponseWriter) (webhook.Store, bool) {
	store, ok := h.store.(webhook.Store)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "webhook storage is not configured")
		return nil, false
	}
	return store, true
}

func (h *Handler) handleCanonicalWebhooks(w http.ResponseWriter, r *http.Request, workspaceID string) {
	store, ok := h.webhookStore(w)
	if !ok {
		return
	}
	includeDeleted := r.URL.Query().Get("include_deleted") == "true"
	var subscriptions []webhook.Subscription
	var err error
	if includeDeleted {
		subscriptions, err = store.ListSubscriptionsIncludingDeleted(r.Context(), workspaceID)
	} else {
		subscriptions, err = store.ListSubscriptions(r.Context(), workspaceID)
	}
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	result := make([]canonicalWebhookSubscription, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		result = append(result, canonicalWebhookSubscriptionFrom(subscription))
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleCanonicalWebhook(w http.ResponseWriter, r *http.Request, workspaceID string, subscriptionID string) {
	store, ok := h.webhookStore(w)
	if !ok {
		return
	}
	subscription, err := store.GetSubscription(r.Context(), workspaceID, strings.TrimSpace(subscriptionID))
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, canonicalWebhookSubscriptionFrom(subscription))
}

func (h *Handler) handleCanonicalCreateWebhook(w http.ResponseWriter, r *http.Request, workspaceID string) {
	store, ok := h.webhookStore(w)
	if !ok {
		return
	}
	var request canonicalWebhookCreateRequest
	if err := readRequiredJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	secret := strings.TrimSpace(request.SigningSecret)
	if secret == "" {
		var err error
		secret, err = newWebhookSigningSecret()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not generate signing secret")
			return
		}
	}
	eventTypes := request.EventTypes
	if len(eventTypes) == 0 {
		eventTypes = []string{controlevent.ReleasePublishedType}
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	actor := firstNonEmpty(requestActorSubject(r), "system")
	created, err := store.CreateSubscription(r.Context(), webhook.Subscription{
		WorkspaceID:   workspaceID,
		Name:          request.Name,
		Endpoint:      request.Endpoint,
		SigningSecret: secret,
		EventTypes:    eventTypes,
		AppKeys:       request.AppKeys,
		Enabled:       enabled,
		CreatedBy:     actor,
	})
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, canonicalWebhookSubscriptionResponse{
		Subscription:  canonicalWebhookSubscriptionFrom(created),
		SigningSecret: secret,
	})
}

func (h *Handler) handleCanonicalUpdateWebhook(w http.ResponseWriter, r *http.Request, workspaceID string, subscriptionID string) {
	store, ok := h.webhookStore(w)
	if !ok {
		return
	}
	var request canonicalWebhookUpdateRequest
	if err := readRequiredJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if request.Name == nil && request.Endpoint == nil && request.SigningSecret == nil && !request.RotateSigningSecret && request.EventTypes == nil && request.AppKeys == nil && request.Enabled == nil {
		writeError(w, http.StatusBadRequest, "at least one webhook setting is required")
		return
	}
	current, err := store.GetSubscription(r.Context(), workspaceID, strings.TrimSpace(subscriptionID))
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	update := current
	if request.Name != nil {
		update.Name = *request.Name
	}
	if request.Endpoint != nil {
		update.Endpoint = *request.Endpoint
	}
	if request.EventTypes != nil {
		update.EventTypes = *request.EventTypes
	}
	if request.AppKeys != nil {
		update.AppKeys = *request.AppKeys
	}
	if request.Enabled != nil {
		update.Enabled = *request.Enabled
	}
	returnedSecret := ""
	if request.SigningSecret != nil {
		returnedSecret = strings.TrimSpace(*request.SigningSecret)
		if returnedSecret == "" {
			writeError(w, http.StatusBadRequest, "signing_secret cannot be empty")
			return
		}
		update.SigningSecret = returnedSecret
	} else if request.RotateSigningSecret {
		returnedSecret, err = newWebhookSigningSecret()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not generate signing secret")
			return
		}
		update.SigningSecret = returnedSecret
	}
	update.UpdatedBy = firstNonEmpty(requestActorSubject(r), "system")
	updated, err := store.UpdateSubscription(r.Context(), update)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, canonicalWebhookSubscriptionResponse{
		Subscription:  canonicalWebhookSubscriptionFrom(updated),
		SigningSecret: returnedSecret,
	})
}

func (h *Handler) handleCanonicalDeleteWebhook(w http.ResponseWriter, r *http.Request, workspaceID string, subscriptionID string) {
	store, ok := h.webhookStore(w)
	if !ok {
		return
	}
	if err := store.DeleteSubscription(r.Context(), workspaceID, strings.TrimSpace(subscriptionID), firstNonEmpty(requestActorSubject(r), "system")); err != nil {
		writeWebhookError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleCanonicalTestWebhook(w http.ResponseWriter, r *http.Request, workspaceID string, subscriptionID string) {
	store, ok := h.webhookStore(w)
	if !ok {
		return
	}
	detail, err := store.CreateTestDelivery(r.Context(), workspaceID, strings.TrimSpace(subscriptionID), firstNonEmpty(requestActorSubject(r), "system"))
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, detail)
}

func (h *Handler) handleCanonicalWebhookDeliveries(w http.ResponseWriter, r *http.Request, workspaceID string, subscriptionID string) {
	store, ok := h.webhookStore(w)
	if !ok {
		return
	}
	query, err := parseCanonicalWebhookDeliveryQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	query.SubscriptionID = strings.TrimSpace(subscriptionID)
	requestedLimit := query.Limit
	query.Limit++
	items, err := store.ListDeliveries(r.Context(), workspaceID, query)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	page := canonicalWebhookDeliveryPage{Items: items}
	if len(items) > requestedLimit {
		page.Items = items[:requestedLimit]
		last := page.Items[len(page.Items)-1].Delivery
		page.NextCursor, err = encodeCanonicalWebhookDeliveryCursor(last.CreatedAt, last.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not encode delivery cursor")
			return
		}
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) handleCanonicalWebhookDelivery(w http.ResponseWriter, r *http.Request, workspaceID string, deliveryID string) {
	store, ok := h.webhookStore(w)
	if !ok {
		return
	}
	detail, err := store.GetDelivery(r.Context(), workspaceID, strings.TrimSpace(deliveryID))
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) handleCanonicalRetryWebhookDelivery(w http.ResponseWriter, r *http.Request, workspaceID string, deliveryID string) {
	store, ok := h.webhookStore(w)
	if !ok {
		return
	}
	deliveryID = strings.TrimSpace(deliveryID)
	if err := store.RetryDelivery(r.Context(), workspaceID, deliveryID, firstNonEmpty(requestActorSubject(r), "system")); err != nil {
		writeWebhookError(w, err)
		return
	}
	detail, err := store.GetDelivery(r.Context(), workspaceID, deliveryID)
	if err != nil {
		writeWebhookError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, detail)
}

func canonicalWebhookSubscriptionFrom(subscription webhook.Subscription) canonicalWebhookSubscription {
	return canonicalWebhookSubscription{
		ID:               subscription.ID,
		WorkspaceID:      subscription.WorkspaceID,
		Name:             subscription.Name,
		EndpointSummary:  webhook.EndpointSummary(subscription.Endpoint),
		HasSigningSecret: subscription.SigningSecret != "",
		EventTypes:       append([]string{}, subscription.EventTypes...),
		AppKeys:          append([]string{}, subscription.AppKeys...),
		Enabled:          subscription.Enabled,
		CreatedBy:        subscription.CreatedBy,
		UpdatedBy:        subscription.UpdatedBy,
		CreatedAt:        subscription.CreatedAt,
		UpdatedAt:        subscription.UpdatedAt,
		DeletedAt:        subscription.DeletedAt,
	}
}

func parseCanonicalWebhookDeliveryQuery(r *http.Request) (webhook.DeliveryListQuery, error) {
	query := webhook.DeliveryListQuery{Limit: 50, State: webhook.DeliveryState(strings.TrimSpace(r.URL.Query().Get("state")))}
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > 100 {
			return webhook.DeliveryListQuery{}, fmt.Errorf("limit must be between 1 and 100")
		}
		query.Limit = limit
	}
	if rawCursor := strings.TrimSpace(r.URL.Query().Get("cursor")); rawCursor != "" {
		cursor, err := decodeCanonicalWebhookDeliveryCursor(rawCursor)
		if err != nil {
			return webhook.DeliveryListQuery{}, fmt.Errorf("invalid delivery cursor")
		}
		query.CursorCreatedAt = cursor.CreatedAt
		query.CursorID = cursor.ID
	}
	if query.State != "" && !webhook.ValidDeliveryState(query.State) {
		return webhook.DeliveryListQuery{}, fmt.Errorf("invalid delivery state %q", query.State)
	}
	return query, nil
}

func encodeCanonicalWebhookDeliveryCursor(createdAt time.Time, id string) (string, error) {
	raw, err := json.Marshal(canonicalWebhookDeliveryCursor{CreatedAt: createdAt.UTC(), ID: id})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeCanonicalWebhookDeliveryCursor(value string) (canonicalWebhookDeliveryCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return canonicalWebhookDeliveryCursor{}, err
	}
	var cursor canonicalWebhookDeliveryCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return canonicalWebhookDeliveryCursor{}, err
	}
	if cursor.CreatedAt.IsZero() || strings.TrimSpace(cursor.ID) == "" {
		return canonicalWebhookDeliveryCursor{}, errors.New("cursor fields are required")
	}
	return cursor, nil
}

func newWebhookSigningSecret() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func writeWebhookError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, webhook.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, webhook.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, webhook.ErrInvalid):
		status = http.StatusBadRequest
	}
	writeError(w, status, err.Error())
}
