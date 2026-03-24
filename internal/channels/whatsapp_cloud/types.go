package whatsapp_cloud

// --- Inbound webhook types (Meta format) ---

// WebhookEnvelope is the top-level structure for Meta WhatsApp Cloud API webhooks.
// Format: {"object":"whatsapp_business_account","entry":[...]}
type WebhookEnvelope struct {
	Object string         `json:"object"`
	Entry  []WebhookEntry `json:"entry"`
}

// WebhookEntry represents a single entry in the webhook payload.
type WebhookEntry struct {
	ID      string          `json:"id"`
	Changes []WebhookChange `json:"changes"`
}

// WebhookChange represents a change notification inside an entry.
type WebhookChange struct {
	Value WebhookValue `json:"value"`
	Field string       `json:"field"`
}

// WebhookValue contains the actual message data or status update.
type WebhookValue struct {
	MessagingProduct string           `json:"messaging_product"`
	Metadata         WebhookMetadata  `json:"metadata"`
	Contacts         []WebhookContact `json:"contacts,omitempty"`
	Messages         []WebhookMessage `json:"messages,omitempty"`
	Statuses         []WebhookStatus  `json:"statuses,omitempty"`
	Errors           []WebhookError   `json:"errors,omitempty"`
}

// WebhookMetadata contains phone number info from the webhook.
type WebhookMetadata struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	PhoneNumberID      string `json:"phone_number_id"`
}

// WebhookContact contains sender contact info.
type WebhookContact struct {
	Profile WebhookProfile `json:"profile"`
	WaID    string         `json:"wa_id"`
}

// WebhookProfile contains the sender's profile name.
type WebhookProfile struct {
	Name string `json:"name"`
}

// WebhookMessage represents an incoming message from the webhook.
type WebhookMessage struct {
	From      string            `json:"from"`
	ID        string            `json:"id"`
	Timestamp string            `json:"timestamp"`
	Type      string            `json:"type"` // text, image, video, audio, document, location, reaction, sticker, contacts, interactive, button
	Text      *WebhookText      `json:"text,omitempty"`
	Image     *WebhookMedia     `json:"image,omitempty"`
	Video     *WebhookMedia     `json:"video,omitempty"`
	Audio     *WebhookMedia     `json:"audio,omitempty"`
	Document  *WebhookMedia     `json:"document,omitempty"`
	Sticker   *WebhookMedia     `json:"sticker,omitempty"`
	Location  *WebhookLocation  `json:"location,omitempty"`
	Reaction  *WebhookReaction  `json:"reaction,omitempty"`
	Context   *WebhookMsgCtx    `json:"context,omitempty"`
	Interactive *WebhookInteractive `json:"interactive,omitempty"`
	Button    *WebhookButton    `json:"button,omitempty"`
}

// WebhookText contains the text body of a message.
type WebhookText struct {
	Body string `json:"body"`
}

// WebhookMedia contains media info (image, video, audio, document, sticker).
type WebhookMedia struct {
	ID       string `json:"id"`
	MimeType string `json:"mime_type,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Caption  string `json:"caption,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// WebhookLocation contains location data.
type WebhookLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Name      string  `json:"name,omitempty"`
	Address   string  `json:"address,omitempty"`
}

// WebhookReaction contains reaction data.
type WebhookReaction struct {
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji"`
}

// WebhookMsgCtx contains quoted/replied message context.
type WebhookMsgCtx struct {
	From      string `json:"from,omitempty"`
	ID        string `json:"id,omitempty"`
	Forwarded bool   `json:"forwarded,omitempty"`
}

// WebhookInteractive contains interactive message reply data.
type WebhookInteractive struct {
	Type      string               `json:"type"`
	ButtonReply *InteractiveReply  `json:"button_reply,omitempty"`
	ListReply   *InteractiveReply  `json:"list_reply,omitempty"`
}

// InteractiveReply holds id/title from button or list interactive replies.
type InteractiveReply struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// WebhookButton contains button reply data.
type WebhookButton struct {
	Text    string `json:"text"`
	Payload string `json:"payload"`
}

// WebhookStatus represents a message status update.
type WebhookStatus struct {
	ID          string `json:"id"`
	Status      string `json:"status"` // sent, delivered, read, failed
	Timestamp   string `json:"timestamp"`
	RecipientID string `json:"recipient_id"`
}

// WebhookError represents an error in the webhook payload.
type WebhookError struct {
	Code    int    `json:"code"`
	Title   string `json:"title"`
	Message string `json:"message,omitempty"`
}

// --- Outbound send request types ---

// SendMessageRequest is the base payload for sending messages via the Cloud API.
type SendMessageRequest struct {
	MessagingProduct string `json:"messaging_product"`
	RecipientType    string `json:"recipient_type,omitempty"`
	To               string `json:"to"`
	Type             string `json:"type"`

	// One of these will be set depending on Type:
	Text     *SendText     `json:"text,omitempty"`
	Image    *SendMedia    `json:"image,omitempty"`
	Video    *SendMedia    `json:"video,omitempty"`
	Audio    *SendMedia    `json:"audio,omitempty"`
	Document *SendMedia    `json:"document,omitempty"`
	Template *SendTemplate `json:"template,omitempty"`
	Reaction *SendReaction `json:"reaction,omitempty"`
}

// SendText is the text payload for outbound messages.
type SendText struct {
	PreviewURL bool   `json:"preview_url,omitempty"`
	Body       string `json:"body"`
}

// SendMedia is the media payload for outbound messages (image, video, audio, document).
type SendMedia struct {
	Link    string `json:"link,omitempty"`
	ID      string `json:"id,omitempty"`
	Caption string `json:"caption,omitempty"`
}

// SendTemplate is the template payload for outbound messages.
type SendTemplate struct {
	Name       string              `json:"name"`
	Language   TemplateLanguage    `json:"language"`
	Components []TemplateComponent `json:"components,omitempty"`
}

// TemplateLanguage specifies the template language.
type TemplateLanguage struct {
	Code string `json:"code"`
}

// TemplateComponent is a component within a template message.
type TemplateComponent struct {
	Type       string              `json:"type"` // header, body, button
	SubType    string              `json:"sub_type,omitempty"`
	Index      string              `json:"index,omitempty"`
	Parameters []TemplateParameter `json:"parameters,omitempty"`
}

// TemplateParameter is a parameter within a template component.
type TemplateParameter struct {
	Type  string `json:"type"` // text, currency, date_time, image, document, video
	Text  string `json:"text,omitempty"`
	Image *SendMedia `json:"image,omitempty"`
}

// SendReaction is the reaction payload for outbound messages.
type SendReaction struct {
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji"`
}

// --- API response types ---

// SendMessageResponse is the response from the Cloud API when sending a message.
type SendMessageResponse struct {
	MessagingProduct string           `json:"messaging_product"`
	Contacts         []ResponseContact `json:"contacts,omitempty"`
	Messages         []ResponseMessage `json:"messages,omitempty"`
	Error            *APIError         `json:"error,omitempty"`
}

// ResponseContact is the contact in a send response.
type ResponseContact struct {
	Input string `json:"input"`
	WaID  string `json:"wa_id"`
}

// ResponseMessage is the message info in a send response.
type ResponseMessage struct {
	ID string `json:"id"`
}

// APIError represents an error response from the Meta API.
type APIError struct {
	Message   string `json:"message"`
	Type      string `json:"type"`
	Code      int    `json:"code"`
	FBTraceID string `json:"fbtrace_id,omitempty"`
}

// --- Read receipt ---

// MarkReadRequest marks a message as read.
type MarkReadRequest struct {
	MessagingProduct string `json:"messaging_product"`
	Status           string `json:"status"`
	MessageID        string `json:"message_id"`
}
