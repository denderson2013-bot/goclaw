package waha

import (
	"encoding/json"
	"fmt"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// wahaCreds maps the credentials JSON from the channel_instances table.
type wahaCreds struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Session string `json:"session"`
}

// wahaInstanceConfig maps the non-secret config JSONB from the channel_instances table.
type wahaInstanceConfig struct {
	DMPolicy    string   `json:"dm_policy,omitempty"`
	GroupPolicy string   `json:"group_policy,omitempty"`
	AllowFrom   []string `json:"allow_from,omitempty"`
	BlockReply  *bool    `json:"block_reply,omitempty"`
	WebhookPath string   `json:"webhook_path,omitempty"`
}

// Factory creates a WAHA channel from DB instance data.
func Factory(name string, creds json.RawMessage, cfg json.RawMessage,
	msgBus *bus.MessageBus, pairingSvc store.PairingStore) (channels.Channel, error) {

	var c wahaCreds
	if len(creds) > 0 {
		if err := json.Unmarshal(creds, &c); err != nil {
			return nil, fmt.Errorf("decode waha credentials: %w", err)
		}
	}
	if c.BaseURL == "" {
		return nil, fmt.Errorf("waha base_url is required")
	}

	var ic wahaInstanceConfig
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &ic); err != nil {
			return nil, fmt.Errorf("decode waha config: %w", err)
		}
	}

	// DB instances default to "pairing" for DMs and groups (secure by default).
	dmPolicy := ic.DMPolicy
	if dmPolicy == "" {
		dmPolicy = "pairing"
	}
	groupPolicy := ic.GroupPolicy
	if groupPolicy == "" {
		groupPolicy = "pairing"
	}

	ch, err := New(c.BaseURL, c.APIKey, c.Session, ic.AllowFrom,
		dmPolicy, groupPolicy, ic.BlockReply, ic.WebhookPath,
		msgBus, pairingSvc)
	if err != nil {
		return nil, err
	}

	ch.SetName(name)
	return ch, nil
}
