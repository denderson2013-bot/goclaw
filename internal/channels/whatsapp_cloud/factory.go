package whatsapp_cloud

import (
	"encoding/json"
	"fmt"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// whatsappCloudCreds maps the credentials JSON from the channel_instances table.
type whatsappCloudCreds struct {
	AccessToken   string `json:"access_token"`
	PhoneNumberID string `json:"phone_number_id"`
	WabaID        string `json:"waba_id"`
	AppSecret     string `json:"app_secret"`
	VerifyToken   string `json:"verify_token"`
}

// whatsappCloudInstanceConfig maps the non-secret config JSONB from the channel_instances table.
type whatsappCloudInstanceConfig struct {
	DMPolicy    string   `json:"dm_policy,omitempty"`
	GroupPolicy string   `json:"group_policy,omitempty"`
	AllowFrom   []string `json:"allow_from,omitempty"`
	BlockReply  *bool    `json:"block_reply,omitempty"`
}

// Factory creates a WhatsApp Cloud API channel from DB instance data.
func Factory(name string, creds json.RawMessage, cfg json.RawMessage,
	msgBus *bus.MessageBus, pairingSvc store.PairingStore) (channels.Channel, error) {

	var c whatsappCloudCreds
	if len(creds) > 0 {
		if err := json.Unmarshal(creds, &c); err != nil {
			return nil, fmt.Errorf("decode whatsapp_cloud credentials: %w", err)
		}
	}
	if c.AccessToken == "" {
		return nil, fmt.Errorf("whatsapp_cloud access_token is required")
	}
	if c.PhoneNumberID == "" {
		return nil, fmt.Errorf("whatsapp_cloud phone_number_id is required")
	}

	var ic whatsappCloudInstanceConfig
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &ic); err != nil {
			return nil, fmt.Errorf("decode whatsapp_cloud config: %w", err)
		}
	}

	// DB instances default to "open" for DMs (BSP model).
	dmPolicy := ic.DMPolicy
	if dmPolicy == "" {
		dmPolicy = "open"
	}
	// No group support in WhatsApp Cloud API.
	groupPolicy := ic.GroupPolicy
	if groupPolicy == "" {
		groupPolicy = "disabled"
	}

	ch, err := New(c.AccessToken, c.PhoneNumberID, c.WabaID, c.AppSecret, c.VerifyToken,
		ic.AllowFrom, dmPolicy, groupPolicy, ic.BlockReply,
		msgBus, pairingSvc)
	if err != nil {
		return nil, err
	}

	ch.SetName(name)
	return ch, nil
}
