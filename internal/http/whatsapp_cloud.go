package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/channels"
	whatsappcloud "github.com/nextlevelbuilder/goclaw/internal/channels/whatsapp_cloud"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func metaGraphAPIBase() string {
	version := os.Getenv("GOCLAW_META_API_VERSION")
	if version == "" {
		version = "v24.0"
	}
	return "https://graph.facebook.com/" + version
}

// WhatsAppCloudHandler handles WhatsApp Cloud API management endpoints.
type WhatsAppCloudHandler struct {
	channelMgr *channels.Manager
	token      string
	httpClient *http.Client
}

// NewWhatsAppCloudHandler creates a new WhatsApp Cloud management handler.
func NewWhatsAppCloudHandler(channelMgr *channels.Manager, token string) *WhatsAppCloudHandler {
	return &WhatsAppCloudHandler{
		channelMgr: channelMgr,
		token:      token,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// RegisterRoutes registers all WhatsApp Cloud management routes.
func (h *WhatsAppCloudHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/whatsapp-cloud/numbers", h.auth(h.handleListNumbers))
	mux.HandleFunc("GET /v1/whatsapp-cloud/templates", h.auth(h.handleListTemplates))
	mux.HandleFunc("POST /v1/whatsapp-cloud/templates", h.auth(h.handleCreateTemplate))
	mux.HandleFunc("POST /v1/whatsapp-cloud/send-template", h.auth(h.handleSendTemplate))
}

func (h *WhatsAppCloudHandler) auth(next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(h.token, "", next)
}

// findCloudChannel finds a running WhatsApp Cloud channel by query param or first available.
func (h *WhatsAppCloudHandler) findCloudChannel(r *http.Request) *whatsappcloud.Channel {
	channelName := r.URL.Query().Get("channel")
	if channelName != "" {
		ch, ok := h.channelMgr.GetChannel(channelName)
		if !ok {
			return nil
		}
		if wc, ok := ch.(*whatsappcloud.Channel); ok {
			return wc
		}
		return nil
	}

	// Find first whatsapp_cloud channel.
	for _, name := range h.channelMgr.GetEnabledChannels() {
		ch, ok := h.channelMgr.GetChannel(name)
		if !ok {
			continue
		}
		if wc, ok := ch.(*whatsappcloud.Channel); ok {
			return wc
		}
	}
	return nil
}

// handleListNumbers lists phone numbers from the Meta WABA.
func (h *WhatsAppCloudHandler) handleListNumbers(w http.ResponseWriter, r *http.Request) {
	ch := h.findCloudChannel(r)
	if ch == nil {
		writeJSON(w, http.StatusOK, map[string]any{"data": []any{}, "message": "Nenhum canal WhatsApp Cloud configurado. Crie um em Canais."})
		return
	}

	wabaID := ch.WabaID()
	if wabaID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "waba_id not configured"})
		return
	}

	url := fmt.Sprintf("%s/%s/phone_numbers", metaGraphAPIBase(), wabaID)
	data, err := h.graphGet(r, url, ch.AccessToken())
	if err != nil {
		slog.Error("whatsapp_cloud.list_numbers", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleListTemplates lists message templates from the Meta WABA.
func (h *WhatsAppCloudHandler) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	ch := h.findCloudChannel(r)
	if ch == nil {
		writeJSON(w, http.StatusOK, map[string]any{"data": []any{}, "message": "Nenhum canal WhatsApp Cloud configurado. Crie um em Canais."})
		return
	}

	wabaID := ch.WabaID()
	if wabaID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "waba_id not configured"})
		return
	}

	url := fmt.Sprintf("%s/%s/message_templates", metaGraphAPIBase(), wabaID)
	data, err := h.graphGet(r, url, ch.AccessToken())
	if err != nil {
		slog.Error("whatsapp_cloud.list_templates", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleCreateTemplate creates a new message template.
func (h *WhatsAppCloudHandler) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	ch := h.findCloudChannel(r)
	if ch == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no whatsapp_cloud channel found"})
		return
	}

	wabaID := ch.WabaID()
	if wabaID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "waba_id not configured"})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	url := fmt.Sprintf("%s/%s/message_templates", metaGraphAPIBase(), wabaID)
	data, err := h.graphPost(r, url, ch.AccessToken(), body)
	if err != nil {
		slog.Error("whatsapp_cloud.create_template", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	w.Write(data)
}

// handleSendTemplate sends a template message to a recipient.
func (h *WhatsAppCloudHandler) handleSendTemplate(w http.ResponseWriter, r *http.Request) {
	locale := store.LocaleFromContext(r.Context())
	_ = locale

	ch := h.findCloudChannel(r)
	if ch == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no whatsapp_cloud channel found"})
		return
	}

	var body struct {
		To           string                            `json:"to"`
		TemplateName string                            `json:"template_name"`
		LanguageCode string                            `json:"language_code"`
		Components   []whatsappcloud.TemplateComponent `json:"components,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if body.To == "" || body.TemplateName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to and template_name are required"})
		return
	}
	if body.LanguageCode == "" {
		body.LanguageCode = "en_US"
	}

	if err := ch.SendTemplate(r.Context(), body.To, body.TemplateName, body.LanguageCode, body.Components); err != nil {
		slog.Error("whatsapp_cloud.send_template", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

// graphGet performs a GET request to the Meta Graph API.
func (h *WhatsAppCloudHandler) graphGet(r *http.Request, url, accessToken string) ([]byte, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graph API request: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("graph API error: status %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

// graphPost performs a POST request to the Meta Graph API.
func (h *WhatsAppCloudHandler) graphPost(r *http.Request, url, accessToken string, body []byte) ([]byte, error) {
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graph API request: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("graph API error: status %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}
