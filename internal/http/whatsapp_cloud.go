package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
	whatsappcloud "github.com/nextlevelbuilder/goclaw/internal/channels/whatsapp_cloud"
	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

const metaEmbeddedSignupCallbackURL = "https://goclaw.focos.ia.br/v1/whatsapp-cloud/callback"

func metaGraphAPIBase() string {
	version := os.Getenv("GOCLAW_META_API_VERSION")
	if version == "" {
		version = "v24.0"
	}
	return "https://graph.facebook.com/" + version
}

// WhatsAppCloudHandler handles WhatsApp Cloud API management endpoints.
type WhatsAppCloudHandler struct {
	channelMgr    *channels.Manager
	token         string
	httpClient    *http.Client
	cfg           *config.Config
	instanceStore store.ChannelInstanceStore
	agentStore    store.AgentStore
	msgBus        *bus.MessageBus
}

// NewWhatsAppCloudHandler creates a new WhatsApp Cloud management handler.
func NewWhatsAppCloudHandler(channelMgr *channels.Manager, token string) *WhatsAppCloudHandler {
	return &WhatsAppCloudHandler{
		channelMgr: channelMgr,
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// SetConfig sets the configuration reference for Meta Embedded Signup.
func (h *WhatsAppCloudHandler) SetConfig(cfg *config.Config) { h.cfg = cfg }

// SetInstanceStore sets the channel instance store for auto-creating instances.
func (h *WhatsAppCloudHandler) SetInstanceStore(s store.ChannelInstanceStore) { h.instanceStore = s }

// SetAgentStore sets the agent store for resolving the default agent.
func (h *WhatsAppCloudHandler) SetAgentStore(s store.AgentStore) { h.agentStore = s }

// SetMessageBus sets the message bus for cache invalidation broadcasts.
func (h *WhatsAppCloudHandler) SetMessageBus(mb *bus.MessageBus) { h.msgBus = mb }

// RegisterRoutes registers all WhatsApp Cloud management routes.
func (h *WhatsAppCloudHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/whatsapp-cloud/numbers", h.auth(h.handleListNumbers))
	mux.HandleFunc("GET /v1/whatsapp-cloud/templates", h.auth(h.handleListTemplates))
	mux.HandleFunc("POST /v1/whatsapp-cloud/templates", h.auth(h.handleCreateTemplate))
	mux.HandleFunc("POST /v1/whatsapp-cloud/send-template", h.auth(h.handleSendTemplate))

	// Embedded Signup endpoints
	mux.HandleFunc("GET /v1/whatsapp-cloud/signup-url", h.auth(h.handleSignupURL))
	mux.HandleFunc("GET /v1/whatsapp-cloud/config", h.auth(h.handleConfig))

	// OAuth callback — NO auth required (Facebook redirects the user's browser here)
	mux.HandleFunc("GET /v1/whatsapp-cloud/callback", h.handleCallback)
}

func (h *WhatsAppCloudHandler) auth(next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(h.token, "", next)
}

// metaConfig returns the Meta Embedded Signup config values from the config object or env.
func (h *WhatsAppCloudHandler) metaConfig() (appID, appSecret, configID, apiVersion string) {
	if h.cfg != nil {
		wc := h.cfg.Channels.WhatsAppCloud
		appID = wc.MetaAppID
		appSecret = wc.MetaAppSecret
		configID = wc.MetaConfigID
		apiVersion = wc.MetaAPIVersion
	}
	// Fallback to env vars
	if appID == "" {
		appID = os.Getenv("GOCLAW_META_APP_ID")
	}
	if appSecret == "" {
		appSecret = os.Getenv("GOCLAW_META_APP_SECRET")
	}
	if configID == "" {
		configID = os.Getenv("GOCLAW_META_CONFIG_ID")
	}
	if apiVersion == "" {
		apiVersion = os.Getenv("GOCLAW_META_API_VERSION")
	}
	if apiVersion == "" {
		apiVersion = "v24.0"
	}
	return
}

// handleSignupURL returns the Facebook OAuth URL for the Embedded Signup flow.
func (h *WhatsAppCloudHandler) handleSignupURL(w http.ResponseWriter, _ *http.Request) {
	appID, _, configID, apiVersion := h.metaConfig()
	if appID == "" || configID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Meta Embedded Signup not configured (GOCLAW_META_APP_ID, GOCLAW_META_CONFIG_ID required)"})
		return
	}

	params := url.Values{
		"client_id":                      {appID},
		"config_id":                      {configID},
		"response_type":                  {"code"},
		"override_default_response_type": {"true"},
		"redirect_uri":                   {metaEmbeddedSignupCallbackURL},
		"scope":                          {"whatsapp_business_management,whatsapp_business_messaging"},
	}

	signupURL := fmt.Sprintf("https://www.facebook.com/%s/dialog/oauth?%s", apiVersion, params.Encode())
	writeJSON(w, http.StatusOK, map[string]string{"url": signupURL})
}

// handleConfig returns public Meta Embedded Signup config (no secrets).
func (h *WhatsAppCloudHandler) handleConfig(w http.ResponseWriter, _ *http.Request) {
	appID, _, configID, apiVersion := h.metaConfig()
	writeJSON(w, http.StatusOK, map[string]any{
		"app_id":       appID,
		"config_id":    configID,
		"api_version":  apiVersion,
		"callback_url": metaEmbeddedSignupCallbackURL,
		"configured":   appID != "" && configID != "",
	})
}

// handleCallback handles the Facebook OAuth callback after Embedded Signup.
// This endpoint does NOT require auth — Facebook redirects the user's browser here.
func (h *WhatsAppCloudHandler) handleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		errMsg := r.URL.Query().Get("error_description")
		if errMsg == "" {
			errMsg = r.URL.Query().Get("error")
		}
		if errMsg == "" {
			errMsg = "no authorization code received"
		}
		slog.Error("whatsapp_cloud.callback: no code", "error", errMsg)
		http.Redirect(w, r, "/whatsapp-cloud?error="+url.QueryEscape(errMsg), http.StatusFound)
		return
	}

	appID, appSecret, _, apiVersion := h.metaConfig()
	if appID == "" || appSecret == "" {
		slog.Error("whatsapp_cloud.callback: missing Meta app credentials")
		http.Redirect(w, r, "/whatsapp-cloud?error="+url.QueryEscape("server misconfigured: missing Meta app credentials"), http.StatusFound)
		return
	}

	ctx := r.Context()
	graphBase := "https://graph.facebook.com/" + apiVersion

	// Step 1: Exchange code for access_token
	accessToken, err := h.exchangeCodeForToken(ctx, graphBase, appID, appSecret, code)
	if err != nil {
		slog.Error("whatsapp_cloud.callback: token exchange failed", "error", err)
		http.Redirect(w, r, "/whatsapp-cloud?error="+url.QueryEscape("token exchange failed: "+err.Error()), http.StatusFound)
		return
	}

	// Step 2: Debug the token to find WABA ID from granular scopes
	wabaID, err := h.getWABAFromDebugToken(ctx, graphBase, appID, appSecret, accessToken)
	if err != nil {
		slog.Warn("whatsapp_cloud.callback: debug_token failed, trying shared WABAs", "error", err)
	}

	// Step 3: If no WABA from debug_token, try shared WABAs endpoint
	if wabaID == "" {
		wabaID, err = h.getWABAFromSharedWABAs(ctx, graphBase, accessToken)
		if err != nil {
			slog.Warn("whatsapp_cloud.callback: shared_wabas failed", "error", err)
		}
	}

	if wabaID == "" {
		slog.Error("whatsapp_cloud.callback: could not determine WABA ID")
		http.Redirect(w, r, "/whatsapp-cloud?error="+url.QueryEscape("could not determine WABA ID from signup"), http.StatusFound)
		return
	}

	// Step 4: Get phone numbers for this WABA
	phoneNumberID, displayPhone, displayName, err := h.getFirstPhoneNumber(ctx, graphBase, wabaID, accessToken)
	if err != nil {
		slog.Warn("whatsapp_cloud.callback: failed to get phone numbers", "error", err)
	}

	// Step 5: Subscribe to webhooks
	if err := h.subscribeApp(ctx, graphBase, wabaID, accessToken); err != nil {
		slog.Warn("whatsapp_cloud.callback: failed to subscribe app", "error", err)
	}

	// Step 6: Auto-create channel instance
	if h.instanceStore != nil && h.agentStore != nil {
		if err := h.createChannelInstance(ctx, accessToken, phoneNumberID, wabaID, displayPhone, displayName); err != nil {
			slog.Error("whatsapp_cloud.callback: failed to create channel instance", "error", err)
			http.Redirect(w, r, "/whatsapp-cloud?error="+url.QueryEscape("signup succeeded but failed to create channel: "+err.Error()), http.StatusFound)
			return
		}
	}

	slog.Info("whatsapp_cloud.callback: embedded signup completed",
		"waba_id", wabaID,
		"phone_number_id", phoneNumberID,
		"display_phone", displayPhone,
	)

	http.Redirect(w, r, "/whatsapp-cloud?success=true", http.StatusFound)
}

// exchangeCodeForToken exchanges the OAuth code for an access token.
func (h *WhatsAppCloudHandler) exchangeCodeForToken(ctx context.Context, graphBase, appID, appSecret, code string) (string, error) {
	tokenURL := fmt.Sprintf("%s/oauth/access_token", graphBase)

	params := url.Values{
		"client_id":     {appID},
		"client_secret": {appSecret},
		"code":          {code},
		"redirect_uri":  {metaEmbeddedSignupCallbackURL},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewBufferString(params.Encode()))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("empty access_token in response: %s", string(body))
	}

	return result.AccessToken, nil
}

// getWABAFromDebugToken tries to extract the WABA ID from debug_token granular scopes.
func (h *WhatsAppCloudHandler) getWABAFromDebugToken(ctx context.Context, graphBase, appID, appSecret, inputToken string) (string, error) {
	debugURL := fmt.Sprintf("%s/debug_token?input_token=%s&access_token=%s|%s",
		graphBase, url.QueryEscape(inputToken), url.QueryEscape(appID), url.QueryEscape(appSecret))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, debugURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data struct {
			GranularScopes []struct {
				Scope       string   `json:"scope"`
				TargetIDs   []string `json:"target_ids"`
			} `json:"granular_scopes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}

	// Look for whatsapp_business_management or whatsapp_business_messaging scope
	for _, gs := range result.Data.GranularScopes {
		if (gs.Scope == "whatsapp_business_management" || gs.Scope == "whatsapp_business_messaging") && len(gs.TargetIDs) > 0 {
			return gs.TargetIDs[0], nil
		}
	}

	return "", fmt.Errorf("no WABA ID found in granular_scopes")
}

// getWABAFromSharedWABAs tries to get the WABA ID from the shared_wabas business endpoint.
func (h *WhatsAppCloudHandler) getWABAFromSharedWABAs(ctx context.Context, graphBase, accessToken string) (string, error) {
	// Try listing businesses first
	bizURL := fmt.Sprintf("%s/me/businesses?access_token=%s", graphBase, url.QueryEscape(accessToken))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bizURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var bizResult struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &bizResult); err != nil {
		return "", err
	}

	// For each business, try to get owned_whatsapp_business_accounts
	for _, biz := range bizResult.Data {
		wabaURL := fmt.Sprintf("%s/%s/owned_whatsapp_business_accounts?access_token=%s",
			graphBase, biz.ID, url.QueryEscape(accessToken))

		wabaReq, err := http.NewRequestWithContext(ctx, http.MethodGet, wabaURL, nil)
		if err != nil {
			continue
		}

		wabaResp, err := h.httpClient.Do(wabaReq)
		if err != nil {
			continue
		}

		wabaBody, _ := io.ReadAll(wabaResp.Body)
		wabaResp.Body.Close()

		if wabaResp.StatusCode >= 400 {
			// Try client_whatsapp_business_accounts as fallback
			wabaURL = fmt.Sprintf("%s/%s/client_whatsapp_business_accounts?access_token=%s",
				graphBase, biz.ID, url.QueryEscape(accessToken))

			wabaReq, err = http.NewRequestWithContext(ctx, http.MethodGet, wabaURL, nil)
			if err != nil {
				continue
			}

			wabaResp, err = h.httpClient.Do(wabaReq)
			if err != nil {
				continue
			}

			wabaBody, _ = io.ReadAll(wabaResp.Body)
			wabaResp.Body.Close()

			if wabaResp.StatusCode >= 400 {
				continue
			}
		}

		var wabaResult struct {
			Data []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
		}
		if json.Unmarshal(wabaBody, &wabaResult) == nil && len(wabaResult.Data) > 0 {
			return wabaResult.Data[0].ID, nil
		}
	}

	return "", fmt.Errorf("no WABA found via business accounts")
}

// getFirstPhoneNumber gets the first phone number from a WABA.
func (h *WhatsAppCloudHandler) getFirstPhoneNumber(ctx context.Context, graphBase, wabaID, accessToken string) (phoneNumberID, displayPhone, displayName string, err error) {
	phoneURL := fmt.Sprintf("%s/%s/phone_numbers?access_token=%s", graphBase, wabaID, url.QueryEscape(accessToken))

	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, phoneURL, nil)
	if reqErr != nil {
		return "", "", "", reqErr
	}

	resp, respErr := h.httpClient.Do(req)
	if respErr != nil {
		return "", "", "", respErr
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", "", "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			ID                string `json:"id"`
			DisplayPhoneNumber string `json:"display_phone_number"`
			VerifiedName      string `json:"verified_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", "", err
	}

	if len(result.Data) > 0 {
		return result.Data[0].ID, result.Data[0].DisplayPhoneNumber, result.Data[0].VerifiedName, nil
	}

	return "", "", "", fmt.Errorf("no phone numbers found for WABA %s", wabaID)
}

// subscribeApp subscribes the app to the WABA for webhook events.
func (h *WhatsAppCloudHandler) subscribeApp(ctx context.Context, graphBase, wabaID, accessToken string) error {
	subURL := fmt.Sprintf("%s/%s/subscribed_apps", graphBase, wabaID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, subURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// createChannelInstance creates a channel_instances row for the newly signed-up WABA.
func (h *WhatsAppCloudHandler) createChannelInstance(ctx context.Context, accessToken, phoneNumberID, wabaID, displayPhone, displayName string) error {
	// Get default agent
	defaultAgent, err := h.agentStore.GetDefault(ctx)
	if err != nil {
		return fmt.Errorf("get default agent: %w", err)
	}

	// Build credentials
	_, _, _, apiVersion := h.metaConfig()
	creds := map[string]string{
		"access_token":    accessToken,
		"phone_number_id": phoneNumberID,
		"waba_id":         wabaID,
	}

	// Include app_secret and verify_token from config if available
	if h.cfg != nil {
		if h.cfg.Channels.WhatsAppCloud.MetaAppSecret != "" {
			creds["app_secret"] = h.cfg.Channels.WhatsAppCloud.MetaAppSecret
		}
		if h.cfg.Channels.WhatsAppCloud.VerifyToken != "" {
			creds["verify_token"] = h.cfg.Channels.WhatsAppCloud.VerifyToken
		}
	}

	credsJSON, _ := json.Marshal(creds)

	cfgMap := map[string]any{
		"dm_policy":    "open",
		"api_version":  apiVersion,
	}
	cfgJSON, _ := json.Marshal(cfgMap)

	instanceName := "whatsapp_cloud"
	if displayPhone != "" {
		instanceName = "whatsapp_cloud/" + displayPhone
	}

	instDisplayName := "WhatsApp Cloud"
	if displayName != "" {
		instDisplayName = displayName
	} else if displayPhone != "" {
		instDisplayName = "WhatsApp " + displayPhone
	}

	inst := &store.ChannelInstanceData{
		Name:        instanceName,
		DisplayName: instDisplayName,
		ChannelType: "whatsapp_cloud",
		AgentID:     defaultAgent.ID,
		Credentials: credsJSON,
		Config:      cfgJSON,
		Enabled:     true,
		CreatedBy:   "embedded_signup",
	}

	if err := h.instanceStore.Create(ctx, inst); err != nil {
		return fmt.Errorf("create instance: %w", err)
	}

	// Broadcast cache invalidation
	if h.msgBus != nil {
		h.msgBus.Broadcast(bus.Event{
			Name:    protocol.EventCacheInvalidate,
			Payload: bus.CacheInvalidatePayload{Kind: bus.CacheKindChannelInstances},
		})
	}

	return nil
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
