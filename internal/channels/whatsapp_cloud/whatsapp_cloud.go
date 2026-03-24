// Package whatsapp_cloud implements the official Meta WhatsApp Cloud API channel.
// It receives messages via Meta webhooks and sends via the Graph API.
package whatsapp_cloud

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

const (
	defaultHTTPTimeout  = 15 * time.Second
	pairingDebounceTime = 60 * time.Second
	windowDuration      = 24 * time.Hour
)

// graphAPIBase returns the Meta Graph API base URL using the configured API version.
func graphAPIBase() string {
	version := os.Getenv("GOCLAW_META_API_VERSION")
	if version == "" {
		version = "v24.0"
	}
	return "https://graph.facebook.com/" + version
}

// Channel implements the official Meta WhatsApp Cloud API channel.
// It receives messages via webhooks and sends via the Graph API.
type Channel struct {
	*channels.BaseChannel

	accessToken   string
	phoneNumberID string
	wabaID        string
	appSecret     string
	verifyToken   string

	httpClient *http.Client

	dmPolicy    string
	groupPolicy string
	blockReply  *bool

	pairingService  store.PairingStore
	pairingDebounce sync.Map // senderID -> time.Time

	// 24h messaging window tracking: senderPhone -> time.Time (last customer message)
	windowTracker sync.Map

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
}

// New creates a new WhatsApp Cloud API channel.
func New(accessToken, phoneNumberID, wabaID, appSecret, verifyToken string,
	allowFrom []string, dmPolicy, groupPolicy string, blockReply *bool,
	msgBus *bus.MessageBus, pairingSvc store.PairingStore) (*Channel, error) {

	if accessToken == "" {
		return nil, fmt.Errorf("whatsapp_cloud access_token is required")
	}
	if phoneNumberID == "" {
		return nil, fmt.Errorf("whatsapp_cloud phone_number_id is required")
	}

	base := channels.NewBaseChannel(channels.TypeWhatsAppCloud, msgBus, allowFrom)
	base.ValidatePolicy(dmPolicy, groupPolicy)

	return &Channel{
		BaseChannel:    base,
		accessToken:    accessToken,
		phoneNumberID:  phoneNumberID,
		wabaID:         wabaID,
		appSecret:      appSecret,
		verifyToken:    verifyToken,
		dmPolicy:       dmPolicy,
		groupPolicy:    groupPolicy,
		blockReply:     blockReply,
		pairingService: pairingSvc,
		httpClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}, nil
}

// BlockReplyEnabled returns the per-channel block_reply override (nil = inherit gateway default).
func (c *Channel) BlockReplyEnabled() *bool { return c.blockReply }

// Start marks the channel as running. WhatsApp Cloud API is webhook-based.
func (c *Channel) Start(ctx context.Context) error {
	slog.Info("starting whatsapp_cloud channel", "phone_number_id", c.phoneNumberID)

	_, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancel = cancel
	c.running = true
	c.mu.Unlock()

	c.SetRunning(true)
	return nil
}

// Stop gracefully shuts down the channel.
func (c *Channel) Stop(_ context.Context) error {
	slog.Info("stopping whatsapp_cloud channel")

	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.running = false
	c.mu.Unlock()

	c.SetRunning(false)
	return nil
}

// Send delivers an outbound message via the Meta Graph API.
func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	// Send media attachments first, if any.
	for _, media := range msg.Media {
		if err := c.sendMedia(ctx, msg.ChatID, media); err != nil {
			slog.Warn("whatsapp_cloud: failed to send media", "chat_id", msg.ChatID, "error", err)
		}
	}

	// Send text content (skip if empty and media was sent).
	if msg.Content == "" {
		return nil
	}

	// Check 24h window.
	c.checkWindow(msg.ChatID)

	return c.sendText(ctx, msg.ChatID, msg.Content)
}

// WebhookHandler returns the HTTP handler for Meta webhooks.
func (c *Channel) WebhookHandler() (string, http.Handler) {
	path := "/webhook/whatsapp-cloud/" + c.Name()
	return path, http.HandlerFunc(c.handleWebhook)
}

// handleWebhook processes incoming Meta WhatsApp Cloud API webhook requests.
func (c *Channel) handleWebhook(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		c.handleVerification(w, r)
	case http.MethodPost:
		c.handleIncoming(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleVerification handles the Meta webhook verification (GET request).
// Meta sends: hub.mode=subscribe&hub.verify_token=xxx&hub.challenge=yyy
func (c *Channel) handleVerification(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	// Check against channel verify_token, or fallback to env var
	expectedToken := c.verifyToken
	if expectedToken == "" {
		expectedToken = os.Getenv("GOCLAW_META_VERIFY_TOKEN")
	}
	if mode == "subscribe" && token != "" && token == expectedToken {
		slog.Info("whatsapp_cloud: webhook verification successful")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(challenge))
		return
	}

	slog.Warn("whatsapp_cloud: webhook verification failed", "mode", mode)
	http.Error(w, "forbidden", http.StatusForbidden)
}

// handleIncoming processes incoming webhook POST requests with messages.
func (c *Channel) handleIncoming(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Warn("whatsapp_cloud: failed to read webhook body", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Validate HMAC signature if app_secret is configured.
	if c.appSecret != "" {
		signature := r.Header.Get("X-Hub-Signature-256")
		if !c.validateSignature(body, signature) {
			slog.Warn("whatsapp_cloud: invalid webhook signature")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	var envelope WebhookEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		slog.Warn("whatsapp_cloud: invalid webhook JSON", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Always respond 200 quickly to Meta.
	w.WriteHeader(http.StatusOK)

	// Process in background to not block the webhook response.
	go c.processEnvelope(envelope)
}

// validateSignature verifies the X-Hub-Signature-256 HMAC.
func (c *Channel) validateSignature(body []byte, signature string) bool {
	if signature == "" {
		return false
	}

	// Signature format: "sha256=<hex>"
	prefix := "sha256="
	if !strings.HasPrefix(signature, prefix) {
		return false
	}

	expectedMAC, err := hex.DecodeString(signature[len(prefix):])
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(c.appSecret))
	mac.Write(body)
	actualMAC := mac.Sum(nil)

	return hmac.Equal(actualMAC, expectedMAC)
}

// processEnvelope processes all messages in a Meta webhook envelope.
func (c *Channel) processEnvelope(envelope WebhookEnvelope) {
	if envelope.Object != "whatsapp_business_account" {
		return
	}

	for _, entry := range envelope.Entry {
		for _, change := range entry.Changes {
			if change.Field != "messages" {
				continue
			}

			value := change.Value

			// Process status updates (just log them).
			for _, status := range value.Statuses {
				slog.Debug("whatsapp_cloud: status update",
					"message_id", status.ID,
					"status", status.Status,
					"recipient", status.RecipientID,
				)
			}

			// Process errors.
			for _, apiErr := range value.Errors {
				slog.Warn("whatsapp_cloud: webhook error",
					"code", apiErr.Code,
					"title", apiErr.Title,
					"message", apiErr.Message,
				)
			}

			// Build contact name map.
			contactNames := make(map[string]string)
			for _, contact := range value.Contacts {
				contactNames[contact.WaID] = contact.Profile.Name
			}

			// Process messages.
			for _, msg := range value.Messages {
				c.handleMessage(msg, contactNames)
			}
		}
	}
}

// handleMessage processes a single incoming WhatsApp message.
func (c *Channel) handleMessage(msg WebhookMessage, contactNames map[string]string) {
	senderID := msg.From
	if senderID == "" {
		return
	}

	// WhatsApp Cloud API is always 1:1 (DM). No group support via Cloud API webhooks.
	chatID := senderID
	peerKind := "direct"

	// Track 24h window: store last customer message time.
	c.windowTracker.Store(senderID, time.Now())

	// Apply DM policy.
	if !c.checkDMPolicy(senderID, chatID) {
		return
	}

	// Extract content based on message type.
	content := c.extractContent(msg)
	if content == "" && msg.Type != "image" && msg.Type != "video" && msg.Type != "audio" && msg.Type != "document" && msg.Type != "sticker" {
		content = "[empty message]"
	}

	// Handle media: download URL from Graph API.
	var media []string
	mediaID := c.extractMediaID(msg)
	if mediaID != "" {
		mediaURL := fmt.Sprintf("%s/%s", graphAPIBase(), mediaID)
		media = append(media, mediaURL)
	}

	metadata := map[string]string{
		"message_id": msg.ID,
	}
	if name, ok := contactNames[senderID]; ok {
		metadata["user_name"] = name
	}

	slog.Debug("whatsapp_cloud message received",
		"sender_id", senderID,
		"chat_id", chatID,
		"type", msg.Type,
		"preview", channels.Truncate(content, 50),
	)

	// Collect contact for processed messages.
	if cc := c.ContactCollector(); cc != nil {
		userName := contactNames[senderID]
		cc.EnsureContact(context.Background(), c.Type(), c.Name(), senderID, senderID, userName, "", peerKind)
	}

	// Send read receipt in background.
	go c.markAsRead(msg.ID)

	c.HandleMessage(senderID, chatID, content, media, metadata, peerKind)
}

// extractContent extracts the text content from a message based on its type.
func (c *Channel) extractContent(msg WebhookMessage) string {
	switch msg.Type {
	case "text":
		if msg.Text != nil {
			return msg.Text.Body
		}
	case "image":
		if msg.Image != nil && msg.Image.Caption != "" {
			return msg.Image.Caption
		}
		return "[image]"
	case "video":
		if msg.Video != nil && msg.Video.Caption != "" {
			return msg.Video.Caption
		}
		return "[video]"
	case "audio":
		return "[audio]"
	case "document":
		if msg.Document != nil {
			if msg.Document.Caption != "" {
				return msg.Document.Caption
			}
			if msg.Document.Filename != "" {
				return fmt.Sprintf("[document: %s]", msg.Document.Filename)
			}
		}
		return "[document]"
	case "sticker":
		return "[sticker]"
	case "location":
		if msg.Location != nil {
			if msg.Location.Name != "" {
				return fmt.Sprintf("[location: %s (%.6f, %.6f)]", msg.Location.Name, msg.Location.Latitude, msg.Location.Longitude)
			}
			return fmt.Sprintf("[location: %.6f, %.6f]", msg.Location.Latitude, msg.Location.Longitude)
		}
	case "reaction":
		if msg.Reaction != nil {
			if msg.Reaction.Emoji == "" {
				return "" // reaction removed, skip
			}
			return fmt.Sprintf("[reaction: %s]", msg.Reaction.Emoji)
		}
	case "interactive":
		if msg.Interactive != nil {
			if msg.Interactive.ButtonReply != nil {
				return msg.Interactive.ButtonReply.Title
			}
			if msg.Interactive.ListReply != nil {
				return msg.Interactive.ListReply.Title
			}
		}
	case "button":
		if msg.Button != nil {
			return msg.Button.Text
		}
	}
	return ""
}

// extractMediaID returns the media ID from a message, if any.
func (c *Channel) extractMediaID(msg WebhookMessage) string {
	switch msg.Type {
	case "image":
		if msg.Image != nil {
			return msg.Image.ID
		}
	case "video":
		if msg.Video != nil {
			return msg.Video.ID
		}
	case "audio":
		if msg.Audio != nil {
			return msg.Audio.ID
		}
	case "document":
		if msg.Document != nil {
			return msg.Document.ID
		}
	case "sticker":
		if msg.Sticker != nil {
			return msg.Sticker.ID
		}
	}
	return ""
}

// sendText sends a text message via the Graph API.
func (c *Channel) sendText(ctx context.Context, to, text string) error {
	req := SendMessageRequest{
		MessagingProduct: "whatsapp",
		To:               to,
		Type:             "text",
		Text:             &SendText{Body: text},
	}
	_, err := c.doSend(ctx, req)
	return err
}

// sendMedia sends a media attachment via the Graph API.
func (c *Channel) sendMedia(ctx context.Context, to string, media bus.MediaAttachment) error {
	ct := strings.ToLower(media.ContentType)

	var req SendMessageRequest
	switch {
	case strings.HasPrefix(ct, "image/"):
		req = SendMessageRequest{
			MessagingProduct: "whatsapp",
			To:               to,
			Type:             "image",
			Image:            &SendMedia{Link: media.URL, Caption: media.Caption},
		}
	case strings.HasPrefix(ct, "video/"):
		req = SendMessageRequest{
			MessagingProduct: "whatsapp",
			To:               to,
			Type:             "video",
			Video:            &SendMedia{Link: media.URL, Caption: media.Caption},
		}
	case strings.HasPrefix(ct, "audio/"):
		req = SendMessageRequest{
			MessagingProduct: "whatsapp",
			To:               to,
			Type:             "audio",
			Audio:            &SendMedia{Link: media.URL},
		}
	default:
		req = SendMessageRequest{
			MessagingProduct: "whatsapp",
			To:               to,
			Type:             "document",
			Document:         &SendMedia{Link: media.URL, Caption: media.Caption},
		}
	}

	_, err := c.doSend(ctx, req)
	return err
}

// SendTemplate sends a template message (for use outside 24h window).
func (c *Channel) SendTemplate(ctx context.Context, to, templateName, languageCode string, components []TemplateComponent) error {
	req := SendMessageRequest{
		MessagingProduct: "whatsapp",
		To:               to,
		Type:             "template",
		Template: &SendTemplate{
			Name:       templateName,
			Language:   TemplateLanguage{Code: languageCode},
			Components: components,
		},
	}
	_, err := c.doSend(ctx, req)
	return err
}

// markAsRead sends a read receipt for a message.
func (c *Channel) markAsRead(messageID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := MarkReadRequest{
		MessagingProduct: "whatsapp",
		Status:           "read",
		MessageID:        messageID,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return
	}

	url := fmt.Sprintf("%s/%s/messages", graphAPIBase(), c.phoneNumberID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.accessToken)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		slog.Debug("whatsapp_cloud: failed to send read receipt", "message_id", messageID, "error", err)
		return
	}
	defer resp.Body.Close()
}

// doSend sends a message request to the Graph API and returns the response.
func (c *Channel) doSend(ctx context.Context, req SendMessageRequest) (*SendMessageResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("whatsapp_cloud marshal: %w", err)
	}

	url := fmt.Sprintf("%s/%s/messages", graphAPIBase(), c.phoneNumberID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("whatsapp_cloud request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.accessToken)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("whatsapp_cloud send: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("whatsapp_cloud send: status %d: %s", resp.StatusCode, string(respBody))
	}

	var sendResp SendMessageResponse
	if err := json.Unmarshal(respBody, &sendResp); err != nil {
		// Non-critical: message was sent but response parse failed.
		slog.Debug("whatsapp_cloud: parse send response failed", "error", err)
		return nil, nil
	}

	if sendResp.Error != nil {
		return nil, fmt.Errorf("whatsapp_cloud API error: [%d] %s", sendResp.Error.Code, sendResp.Error.Message)
	}

	return &sendResp, nil
}

// checkWindow logs a warning if the 24h messaging window is closed.
func (c *Channel) checkWindow(senderID string) {
	val, ok := c.windowTracker.Load(senderID)
	if !ok {
		slog.Warn("whatsapp_cloud: no customer message recorded, 24h window may be closed",
			"recipient", senderID)
		return
	}
	lastMsg := val.(time.Time)
	if time.Since(lastMsg) > windowDuration {
		slog.Warn("whatsapp_cloud: 24h messaging window likely closed, send may fail",
			"recipient", senderID,
			"last_customer_msg", lastMsg.Format(time.RFC3339),
		)
	}
}

// --- DM policy (WhatsApp Cloud API is DM-only) ---

// checkDMPolicy evaluates the DM policy for a sender, handling pairing flow.
func (c *Channel) checkDMPolicy(senderID, chatID string) bool {
	dmPolicy := c.dmPolicy
	if dmPolicy == "" {
		dmPolicy = "open"
	}

	switch dmPolicy {
	case "disabled":
		slog.Debug("whatsapp_cloud DM rejected: disabled", "sender_id", senderID)
		return false
	case "open":
		return true
	case "allowlist":
		if !c.IsAllowed(senderID) {
			slog.Debug("whatsapp_cloud DM rejected by allowlist", "sender_id", senderID)
			return false
		}
		return true
	default: // "pairing"
		paired := false
		if c.pairingService != nil {
			p, err := c.pairingService.IsPaired(senderID, c.Name())
			if err != nil {
				slog.Warn("security.pairing_check_failed, assuming paired (fail-open)",
					"sender_id", senderID, "channel", c.Name(), "error", err)
				paired = true
			} else {
				paired = p
			}
		}
		inAllowList := c.HasAllowList() && c.IsAllowed(senderID)

		if paired || inAllowList {
			return true
		}

		c.sendPairingReply(senderID, chatID)
		return false
	}
}

// sendPairingReply sends a pairing code via the Cloud API.
func (c *Channel) sendPairingReply(senderID, chatID string) {
	if c.pairingService == nil {
		return
	}

	// Debounce.
	if lastSent, ok := c.pairingDebounce.Load(senderID); ok {
		if time.Since(lastSent.(time.Time)) < pairingDebounceTime {
			return
		}
	}

	code, err := c.pairingService.RequestPairing(senderID, c.Name(), chatID, "default", nil)
	if err != nil {
		slog.Debug("whatsapp_cloud pairing request failed", "sender_id", senderID, "error", err)
		return
	}

	replyText := fmt.Sprintf(
		"GoClaw: access not configured.\n\nYour WhatsApp ID: %s\n\nPairing code: %s\n\nAsk the bot owner to approve with:\n  goclaw pairing approve %s",
		senderID, code, code,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.sendText(ctx, chatID, replyText); err != nil {
		slog.Warn("whatsapp_cloud: failed to send pairing reply", "error", err)
	} else {
		c.pairingDebounce.Store(senderID, time.Now())
		slog.Info("whatsapp_cloud pairing reply sent", "sender_id", senderID, "code", code)
	}
}

// AccessToken returns the channel's access token (for HTTP API endpoints).
func (c *Channel) AccessToken() string { return c.accessToken }

// WabaID returns the WhatsApp Business Account ID.
func (c *Channel) WabaID() string { return c.wabaID }

// PhoneNumberID returns the phone number ID.
func (c *Channel) PhoneNumberID() string { return c.phoneNumberID }

// Ensure Channel implements the required interfaces at compile time.
var _ channels.Channel = (*Channel)(nil)
var _ channels.WebhookChannel = (*Channel)(nil)
