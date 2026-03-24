package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// WahaSessionsHandler proxies WAHA session management requests.
// All WAHA credentials come from server-side config (env vars), never from the frontend.
type WahaSessionsHandler struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	ciStore    store.ChannelInstanceStore
	msgBus     *bus.MessageBus
	encKey     string
	token      string // gateway auth token
}

// NewWahaSessionsHandler creates a new WAHA sessions handler.
func NewWahaSessionsHandler(baseURL, apiKey, token string, ciStore store.ChannelInstanceStore, msgBus *bus.MessageBus, encKey string) *WahaSessionsHandler {
	return &WahaSessionsHandler{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		ciStore: ciStore,
		msgBus:  msgBus,
		encKey:  encKey,
		token:   token,
	}
}

// RegisterRoutes registers all WAHA session management routes on the given mux.
func (h *WahaSessionsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/waha/sessions", h.auth(h.handleListSessions))
	mux.HandleFunc("POST /v1/waha/sessions", h.auth(h.handleCreateSession))
	mux.HandleFunc("GET /v1/waha/sessions/{name}", h.auth(h.handleGetSession))
	mux.HandleFunc("DELETE /v1/waha/sessions/{name}", h.auth(h.handleDeleteSession))
	mux.HandleFunc("POST /v1/waha/sessions/{name}/start", h.auth(h.handleStartSession))
	mux.HandleFunc("POST /v1/waha/sessions/{name}/stop", h.auth(h.handleStopSession))
	mux.HandleFunc("GET /v1/waha/sessions/{name}/qr", h.authOrQuery(h.handleGetQR))
	mux.HandleFunc("POST /v1/waha/sessions/{name}/link", h.auth(h.handleLinkSession))
}

func (h *WahaSessionsHandler) auth(next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(h.token, "", next)
}

// authOrQuery allows auth via Bearer header OR ?token= query param (for <img> tags).
func (h *WahaSessionsHandler) authOrQuery(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Try header first
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			requireAuth(h.token, "", next)(w, r)
			return
		}
		// Fallback to query param
		qToken := r.URL.Query().Get("token")
		if qToken != "" && qToken == h.token {
			next(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}

// proxyGET sends a GET request to WAHA and writes the response back.
func (h *WahaSessionsHandler) proxyGET(w http.ResponseWriter, wahaPath string) {
	req, err := http.NewRequest(http.MethodGet, h.baseURL+wahaPath, nil)
	if err != nil {
		slog.Error("waha_sessions.proxy_get", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create request"})
		return
	}
	req.Header.Set("X-Api-Key", h.apiKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		slog.Error("waha_sessions.proxy_get", "url", wahaPath, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to reach WAHA"})
		return
	}
	defer resp.Body.Close()

	// Copy content type and status
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// proxyPOST sends a POST request to WAHA with the given body and writes the response back.
func (h *WahaSessionsHandler) proxyPOST(w http.ResponseWriter, wahaPath string, body any) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			slog.Error("waha_sessions.proxy_post_marshal", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to marshal body"})
			return
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(http.MethodPost, h.baseURL+wahaPath, bodyReader)
	if err != nil {
		slog.Error("waha_sessions.proxy_post", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create request"})
		return
	}
	req.Header.Set("X-Api-Key", h.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		slog.Error("waha_sessions.proxy_post", "url", wahaPath, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to reach WAHA"})
		return
	}
	defer resp.Body.Close()

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// proxyDELETE sends a DELETE request to WAHA and writes the response back.
func (h *WahaSessionsHandler) proxyDELETE(w http.ResponseWriter, wahaPath string) {
	req, err := http.NewRequest(http.MethodDelete, h.baseURL+wahaPath, nil)
	if err != nil {
		slog.Error("waha_sessions.proxy_delete", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create request"})
		return
	}
	req.Header.Set("X-Api-Key", h.apiKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		slog.Error("waha_sessions.proxy_delete", "url", wahaPath, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to reach WAHA"})
		return
	}
	defer resp.Body.Close()

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// handleListSessions lists all WAHA sessions.
func (h *WahaSessionsHandler) handleListSessions(w http.ResponseWriter, _ *http.Request) {
	h.proxyGET(w, "/api/sessions")
}

// handleCreateSession creates a new WAHA session with webhook config.
func (h *WahaSessionsHandler) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}

	// Build webhook URL from request Host header
	scheme := "https"
	if r.TLS == nil {
		// Check for forwarded proto
		if fp := r.Header.Get("X-Forwarded-Proto"); fp != "" {
			scheme = fp
		} else {
			scheme = "http"
		}
	}
	webhookURL := fmt.Sprintf("%s://%s/webhook/waha/%s", scheme, r.Host, body.Name)

	wahaBody := map[string]any{
		"name":  body.Name,
		"start": true,
		"config": map[string]any{
			"webhooks": []map[string]any{
				{
					"url":    webhookURL,
					"events": []string{"message", "message.any", "session.status"},
					"retries": map[string]any{
						"delaySeconds": 2,
						"attempts":     15,
						"policy":       "constant",
					},
				},
			},
		},
	}

	slog.Info("waha_sessions.create", "name", body.Name, "webhook_url", webhookURL)
	h.proxyPOST(w, "/api/sessions", wahaBody)
}

// handleGetSession gets details for a specific WAHA session.
func (h *WahaSessionsHandler) handleGetSession(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session name is required"})
		return
	}
	h.proxyGET(w, "/api/sessions/"+name)
}

// handleDeleteSession deletes a WAHA session.
func (h *WahaSessionsHandler) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session name is required"})
		return
	}
	h.proxyDELETE(w, "/api/sessions/"+name)
}

// handleStartSession starts a WAHA session.
func (h *WahaSessionsHandler) handleStartSession(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session name is required"})
		return
	}
	h.proxyPOST(w, "/api/sessions/"+name+"/start", nil)
}

// handleStopSession stops a WAHA session.
func (h *WahaSessionsHandler) handleStopSession(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session name is required"})
		return
	}
	h.proxyPOST(w, "/api/sessions/"+name+"/stop", nil)
}

// handleGetQR proxies the QR code image from WAHA.
func (h *WahaSessionsHandler) handleGetQR(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session name is required"})
		return
	}
	h.proxyGET(w, "/api/"+name+"/auth/qr")
}

// handleLinkSession creates a channel instance linked to an agent for this WAHA session.
func (h *WahaSessionsHandler) handleLinkSession(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session name is required"})
		return
	}

	var body struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if body.AgentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id is required"})
		return
	}

	agentID, err := uuid.Parse(body.AgentID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid agent_id"})
		return
	}

	if h.ciStore == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "channel instance store not available"})
		return
	}

	// Build credentials JSON with WAHA base_url, api_key, and session name
	creds := map[string]string{
		"base_url": h.baseURL,
		"api_key":  h.apiKey,
		"session":  name,
	}
	credsJSON, err := json.Marshal(creds)
	if err != nil {
		slog.Error("waha_sessions.link_marshal_creds", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to marshal credentials"})
		return
	}

	// Build config with dm_policy open
	configJSON, _ := json.Marshal(map[string]string{
		"dm_policy": "open",
	})

	instanceName := "waha/" + name
	inst := &store.ChannelInstanceData{
		Name:        instanceName,
		DisplayName: "WAHA " + name,
		ChannelType: "waha",
		AgentID:     agentID,
		Credentials: credsJSON,
		Config:      configJSON,
		Enabled:     true,
		CreatedBy:   "waha-sessions-ui",
	}

	if err := h.ciStore.Create(r.Context(), inst); err != nil {
		slog.Error("waha_sessions.link_create", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create channel instance: " + err.Error()})
		return
	}

	// Broadcast cache invalidation to trigger channel reload
	if h.msgBus != nil {
		h.msgBus.Broadcast(bus.Event{
			Name:    protocol.EventCacheInvalidate,
			Payload: bus.CacheInvalidatePayload{Kind: bus.CacheKindChannelInstances},
		})
	}

	slog.Info("waha_sessions.linked", "session", name, "agent_id", body.AgentID, "instance_id", inst.ID.String())
	writeJSON(w, http.StatusCreated, map[string]any{
		"status":      "linked",
		"instance_id": inst.ID.String(),
		"name":        instanceName,
	})
}
