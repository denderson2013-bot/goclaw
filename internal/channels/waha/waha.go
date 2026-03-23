// Package waha implements the WAHA (WhatsApp HTTP API) channel.
// WAHA is a self-hosted WhatsApp gateway that exposes REST endpoints
// for sending messages and delivers inbound messages via webhooks.
package waha

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

const (
	defaultHTTPTimeout  = 10 * time.Second
	pairingDebounceTime = 60 * time.Second
)

// Channel implements the WAHA WhatsApp HTTP API channel.
// It receives messages via webhooks and sends via REST API.
type Channel struct {
	*channels.BaseChannel

	baseURL    string
	apiKey     string
	session    string
	httpClient *http.Client

	dmPolicy    string
	groupPolicy string
	blockReply  *bool
	webhookPath string

	pairingService  store.PairingStore
	pairingDebounce sync.Map // senderID -> time.Time
	approvedGroups  sync.Map // chatID -> true

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
}

// New creates a new WAHA channel.
func New(baseURL, apiKey, session string, allowFrom []string,
	dmPolicy, groupPolicy string, blockReply *bool, webhookPath string,
	msgBus *bus.MessageBus, pairingSvc store.PairingStore) (*Channel, error) {

	if baseURL == "" {
		return nil, fmt.Errorf("waha base_url is required")
	}
	if session == "" {
		session = "default"
	}

	// Strip trailing slash from baseURL
	baseURL = strings.TrimRight(baseURL, "/")

	base := channels.NewBaseChannel(channels.TypeWaha, msgBus, allowFrom)
	base.ValidatePolicy(dmPolicy, groupPolicy)

	return &Channel{
		BaseChannel:    base,
		baseURL:        baseURL,
		apiKey:         apiKey,
		session:        session,
		dmPolicy:       dmPolicy,
		groupPolicy:    groupPolicy,
		blockReply:     blockReply,
		webhookPath:    webhookPath,
		pairingService: pairingSvc,
		httpClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}, nil
}

// BlockReplyEnabled returns the per-channel block_reply override (nil = inherit gateway default).
func (c *Channel) BlockReplyEnabled() *bool { return c.blockReply }

// Start marks the channel as running. WAHA is webhook-based, so no polling is needed.
func (c *Channel) Start(ctx context.Context) error {
	slog.Info("starting waha channel", "base_url", c.baseURL, "session", c.session)

	_, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancel = cancel
	c.running = true
	c.mu.Unlock()

	c.SetRunning(true)
	return nil
}

// Stop gracefully shuts down the WAHA channel.
func (c *Channel) Stop(_ context.Context) error {
	slog.Info("stopping waha channel")

	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.running = false
	c.mu.Unlock()

	c.SetRunning(false)
	return nil
}

// Send delivers an outbound message via the WAHA HTTP API.
func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	// Send media attachments first, if any.
	for _, media := range msg.Media {
		if err := c.sendMedia(ctx, msg.ChatID, media); err != nil {
			slog.Warn("waha: failed to send media", "chat_id", msg.ChatID, "error", err)
		}
	}

	// Send text content (skip if empty and media was sent).
	if msg.Content == "" && len(msg.Media) > 0 {
		return nil
	}
	if msg.Content == "" {
		return nil
	}

	return c.sendText(ctx, msg.ChatID, msg.Content)
}

// sendText sends a text message via WAHA.
func (c *Channel) sendText(ctx context.Context, chatID, text string) error {
	req := SendTextRequest{
		Session: c.session,
		ChatID:  chatID,
		Text:    text,
	}
	return c.doPost(ctx, "/api/sendText", req)
}

// sendMedia sends a media attachment using the appropriate WAHA endpoint.
func (c *Channel) sendMedia(ctx context.Context, chatID string, media bus.MediaAttachment) error {
	ct := strings.ToLower(media.ContentType)

	switch {
	case strings.HasPrefix(ct, "image/"):
		req := SendImageRequest{
			Session: c.session,
			ChatID:  chatID,
			File: FilePayload{
				URL:      media.URL,
				MimeType: media.ContentType,
			},
			Caption: media.Caption,
		}
		return c.doPost(ctx, "/api/sendImage", req)

	case strings.HasPrefix(ct, "video/"):
		req := SendVideoRequest{
			Session: c.session,
			ChatID:  chatID,
			File: FilePayload{
				URL:      media.URL,
				MimeType: media.ContentType,
			},
			Caption: media.Caption,
		}
		return c.doPost(ctx, "/api/sendVideo", req)

	case strings.HasPrefix(ct, "audio/"):
		req := SendVoiceRequest{
			Session: c.session,
			ChatID:  chatID,
			File: FilePayload{
				URL:      media.URL,
				MimeType: media.ContentType,
			},
		}
		return c.doPost(ctx, "/api/sendVoice", req)

	default:
		// Fall back to generic file send.
		req := SendFileRequest{
			Session: c.session,
			ChatID:  chatID,
			File: FilePayload{
				URL:      media.URL,
				MimeType: media.ContentType,
			},
		}
		return c.doPost(ctx, "/api/sendFile", req)
	}
}

// WebhookHandler returns the HTTP handler for receiving WAHA webhook events.
func (c *Channel) WebhookHandler() (string, http.Handler) {
	path := c.webhookPath
	if path == "" {
		path = "/webhook/waha/" + c.Name()
	}
	return path, http.HandlerFunc(c.handleWebhook)
}

// handleWebhook processes incoming WAHA webhook HTTP requests.
func (c *Channel) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Warn("waha: failed to read webhook body", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var envelope WebhookEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		slog.Warn("waha: invalid webhook JSON", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Only process message events.
	if envelope.Event != "message" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Skip own messages (fromMe).
	if envelope.Payload.FromMe {
		w.WriteHeader(http.StatusOK)
		return
	}

	c.handleIncomingMessage(envelope)

	w.WriteHeader(http.StatusOK)
}

// handleIncomingMessage processes a single inbound WAHA message.
func (c *Channel) handleIncomingMessage(env WebhookEnvelope) {
	payload := env.Payload

	rawFrom := payload.From
	if rawFrom == "" {
		return
	}

	// Determine chat ID: use "to" for DMs (messages sent to us), "from" for groups.
	chatID := rawFrom

	// Detect DM vs group by suffix.
	peerKind := "direct"
	if strings.HasSuffix(rawFrom, "@g.us") {
		peerKind = "group"
		chatID = rawFrom // group chat ID
	}

	// Strip @c.us / @g.us suffix to get clean sender ID.
	senderID := stripWASuffix(rawFrom)
	if peerKind == "group" {
		// In group messages, "from" is the group. We need the sender.
		// WAHA group messages have "from" = group@g.us, individual sender not always available.
		// Use the "from" field as-is for groups (the group ID is the chat ID).
		// For sender tracking in groups, WAHA may put sender in payload differently.
		// We use the group chatID as the sender context for policy checks.
		senderID = stripWASuffix(rawFrom)
	}

	// Apply DM/group policy.
	if peerKind == "direct" {
		if !c.checkDMPolicy(senderID, chatID) {
			return
		}
	} else {
		if !c.checkGroupPolicy(senderID, chatID) {
			slog.Debug("waha group message rejected by policy", "sender_id", senderID)
			return
		}
	}

	content := payload.Body
	if content == "" && !payload.HasMedia {
		content = "[empty message]"
	}

	// Handle media.
	var media []string
	if payload.HasMedia && payload.Media != nil && payload.Media.URL != "" {
		media = append(media, payload.Media.URL)
	}

	metadata := map[string]string{
		"message_id": payload.ID,
		"session":    env.Session,
	}

	slog.Debug("waha message received",
		"sender_id", senderID,
		"chat_id", chatID,
		"peer_kind", peerKind,
		"preview", channels.Truncate(content, 50),
	)

	// Collect contact for processed messages.
	if cc := c.ContactCollector(); cc != nil {
		cc.EnsureContact(context.Background(), c.Type(), c.Name(), senderID, senderID, "", "", peerKind)
	}

	// Send typing indicator and read receipt in the background.
	go c.sendTypingAndSeen(chatID)

	c.HandleMessage(senderID, chatID, content, media, metadata, peerKind)
}

// sendTypingAndSeen sends a typing indicator and read receipt for the chat.
func (c *Channel) sendTypingAndSeen(chatID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Typing indicator.
	presenceReq := PresenceRequest{
		ChatID:   chatID,
		Presence: "typing",
	}
	endpoint := fmt.Sprintf("/api/%s/presence", c.session)
	if err := c.doPost(ctx, endpoint, presenceReq); err != nil {
		slog.Debug("waha: failed to send typing indicator", "chat_id", chatID, "error", err)
	}

	// Read receipt.
	seenReq := SendSeenRequest{
		Session: c.session,
		ChatID:  chatID,
	}
	if err := c.doPost(ctx, "/api/sendSeen", seenReq); err != nil {
		slog.Debug("waha: failed to send read receipt", "chat_id", chatID, "error", err)
	}
}

// checkDMPolicy evaluates the DM policy for a sender, handling pairing flow.
func (c *Channel) checkDMPolicy(senderID, chatID string) bool {
	dmPolicy := c.dmPolicy
	if dmPolicy == "" {
		dmPolicy = "pairing"
	}

	switch dmPolicy {
	case "disabled":
		slog.Debug("waha DM rejected: disabled", "sender_id", senderID)
		return false
	case "open":
		return true
	case "allowlist":
		if !c.IsAllowed(senderID) {
			slog.Debug("waha DM rejected by allowlist", "sender_id", senderID)
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

// checkGroupPolicy evaluates the group policy for a sender, with pairing support.
func (c *Channel) checkGroupPolicy(senderID, chatID string) bool {
	groupPolicy := c.groupPolicy
	if groupPolicy == "" {
		groupPolicy = "open"
	}

	switch groupPolicy {
	case "disabled":
		return false
	case "allowlist":
		return c.IsAllowed(senderID)
	case "pairing":
		if c.IsAllowed(senderID) {
			return true
		}
		if _, cached := c.approvedGroups.Load(chatID); cached {
			return true
		}
		groupSenderID := fmt.Sprintf("group:%s", chatID)
		if c.pairingService != nil {
			paired, err := c.pairingService.IsPaired(groupSenderID, c.Name())
			if err != nil {
				slog.Warn("security.pairing_check_failed, assuming paired (fail-open)",
					"group_sender", groupSenderID, "channel", c.Name(), "error", err)
				paired = true
			}
			if paired {
				c.approvedGroups.Store(chatID, true)
				return true
			}
		}
		c.sendPairingReply(groupSenderID, chatID)
		return false
	default: // "open"
		return true
	}
}

// sendPairingReply sends a pairing code via the WAHA API.
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
		slog.Debug("waha pairing request failed", "sender_id", senderID, "error", err)
		return
	}

	replyText := fmt.Sprintf(
		"GoClaw: access not configured.\n\nYour WhatsApp ID: %s\n\nPairing code: %s\n\nAsk the bot owner to approve with:\n  goclaw pairing approve %s",
		senderID, code, code,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.sendText(ctx, chatID, replyText); err != nil {
		slog.Warn("waha: failed to send pairing reply", "error", err)
	} else {
		c.pairingDebounce.Store(senderID, time.Now())
		slog.Info("waha pairing reply sent", "sender_id", senderID, "code", code)
	}
}

// doPost sends a JSON POST request to the WAHA API.
func (c *Channel) doPost(ctx context.Context, path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("waha marshal: %w", err)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("waha request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-Api-Key", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("waha POST %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("waha POST %s: status %d: %s", path, resp.StatusCode, string(respBody))
	}

	return nil
}

// stripWASuffix removes @c.us or @g.us suffix from a WhatsApp ID.
func stripWASuffix(id string) string {
	if idx := strings.Index(id, "@"); idx > 0 {
		return id[:idx]
	}
	return id
}
