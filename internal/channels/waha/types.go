package waha

// WebhookEnvelope is the top-level JSON structure sent by WAHA webhook events.
type WebhookEnvelope struct {
	Event   string         `json:"event"`
	Session string         `json:"session"`
	Me      WebhookMe      `json:"me"`
	Payload WebhookPayload `json:"payload"`
}

// WebhookMe identifies the bot's own WhatsApp account.
type WebhookMe struct {
	ID string `json:"id"`
}

// WebhookPayload contains the message data inside a webhook event.
type WebhookPayload struct {
	ID        string        `json:"id"`
	Timestamp int64         `json:"timestamp"`
	From      string        `json:"from"`
	FromMe    bool          `json:"fromMe"`
	To        string        `json:"to"`
	Body      string        `json:"body"`
	HasMedia  bool          `json:"hasMedia"`
	Media     *WebhookMedia `json:"media,omitempty"`
}

// WebhookMedia describes a media attachment in a webhook message.
type WebhookMedia struct {
	URL      string `json:"url"`
	MimeType string `json:"mimetype"`
	Filename string `json:"filename,omitempty"`
}

// --- Outbound request types ---

// SendTextRequest is the payload for POST /api/sendText.
type SendTextRequest struct {
	Session string `json:"session"`
	ChatID  string `json:"chatId"`
	Text    string `json:"text"`
}

// FilePayload describes a file for media send endpoints.
type FilePayload struct {
	URL      string `json:"url"`
	MimeType string `json:"mimetype,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// SendImageRequest is the payload for POST /api/sendImage.
type SendImageRequest struct {
	Session string      `json:"session"`
	ChatID  string      `json:"chatId"`
	File    FilePayload `json:"file"`
	Caption string      `json:"caption,omitempty"`
}

// SendFileRequest is the payload for POST /api/sendFile.
type SendFileRequest struct {
	Session string      `json:"session"`
	ChatID  string      `json:"chatId"`
	File    FilePayload `json:"file"`
}

// SendVoiceRequest is the payload for POST /api/sendVoice.
type SendVoiceRequest struct {
	Session string      `json:"session"`
	ChatID  string      `json:"chatId"`
	File    FilePayload `json:"file"`
}

// SendVideoRequest is the payload for POST /api/sendVideo.
type SendVideoRequest struct {
	Session string      `json:"session"`
	ChatID  string      `json:"chatId"`
	File    FilePayload `json:"file"`
	Caption string      `json:"caption,omitempty"`
}

// PresenceRequest is the payload for POST /api/{session}/presence.
type PresenceRequest struct {
	ChatID   string `json:"chatId"`
	Presence string `json:"presence"`
}

// SendSeenRequest is the payload for POST /api/sendSeen.
type SendSeenRequest struct {
	Session string `json:"session"`
	ChatID  string `json:"chatId"`
}

// ReactionRequest is the payload for POST /api/reaction.
type ReactionRequest struct {
	Session   string `json:"session"`
	ChatID    string `json:"chatId"`
	MessageID string `json:"messageId"`
	Emoji     string `json:"emoji"`
}
