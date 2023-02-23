package config

import (
	"bytes"
	"strconv"
	"strings"
	"text/template"

	skype "github.com/kelaresg/go-skypeapi"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/kelaresg/matrix-skype/types"
)

type BridgeConfig struct {
	UsernameTemplate    string `yaml:"username_template"`
	DisplaynameTemplate string `yaml:"displayname_template"`
	CommunityTemplate   string `yaml:"community_template"`

	ConnectionTimeout     int  `yaml:"connection_timeout"`
	FetchMessageOnTimeout bool `yaml:"fetch_message_on_timeout"`
	DeliveryReceipts      bool `yaml:"delivery_receipts"`
	LoginQRRegenCount     int  `yaml:"login_qr_regen_count"`
	MaxConnectionAttempts int  `yaml:"max_connection_attempts"`
	ConnectionRetryDelay  int  `yaml:"connection_retry_delay"`
	ReportConnectionRetry bool `yaml:"report_connection_retry"`
	ReportConnectionSuccess	bool `yaml:"report_connection_success"`
	ChatListWait          int  `yaml:"chat_list_wait"`
	PortalSyncWait        int  `yaml:"portal_sync_wait"`

	CallNotices struct {
		Start bool `yaml:"start"`
		End   bool `yaml:"end"`
	} `yaml:"call_notices"`

	InitialChatSync      int    `yaml:"initial_chat_sync_count"`
	InitialHistoryFill   int    `yaml:"initial_history_fill_count"`
	HistoryDisableNotifs bool   `yaml:"initial_history_disable_notifications"`
	RecoverChatSync      int    `yaml:"recovery_chat_sync_count"`
	RecoverHistory       bool   `yaml:"recovery_history_backfill"`
	SyncChatMaxAge       uint64 `yaml:"sync_max_chat_age"`
	SyncContact          bool   `yaml:"sync_contact"`

	SyncWithCustomPuppets bool   `yaml:"sync_with_custom_puppets"`

	InviteOwnPuppetForBackfilling bool `yaml:"invite_own_puppet_for_backfilling"`
	PrivateChatPortalMeta         bool `yaml:"private_chat_portal_meta"`

	DoublePuppetServerMap      map[string]string `yaml:"double_puppet_server_map"`
	DoublePuppetAllowDiscovery bool              `yaml:"double_puppet_allow_discovery"`
	LoginSharedSecretMap       map[string]string `yaml:"login_shared_secret_map"`

	WhatsappThumbnail bool `yaml:"whatsapp_thumbnail"`

	AllowUserInvite bool `yaml:"allow_user_invite"`

	CommandPrefix string `yaml:"command_prefix"`

	Encryption struct {
		Allow   bool `yaml:"allow"`
		Default bool `yaml:"default"`

		KeySharing struct {
			Allow               bool `yaml:"allow"`
			RequireCrossSigning bool `yaml:"require_cross_signing"`
			RequireVerification bool `yaml:"require_verification"`
		} `yaml:"key_sharing"`

		PuppetId struct {
			Allow                  bool `yaml:"allow"`
			Key                    string `yaml:"key"`
			UsernameTemplatePrefix string `yaml:"username_template_prefix"`
		} `yaml:"puppet_id"`
	} `yaml:"encryption"`

	Permissions PermissionConfig `yaml:"permissions"`

	Relaybot RelaybotConfig `yaml:"relaybot"`

	usernameTemplate    *template.Template `yaml:"-"`
	displaynameTemplate *template.Template `yaml:"-"`
	communityTemplate   *template.Template `yaml:"-"`
}

func (bc *BridgeConfig) setDefaults() {
	bc.ConnectionTimeout = 20
	bc.FetchMessageOnTimeout = false
	bc.DeliveryReceipts = false
	bc.LoginQRRegenCount = 2
	bc.MaxConnectionAttempts = 3
	bc.ConnectionRetryDelay = -1
	bc.ReportConnectionRetry = true
	bc.ReportConnectionSuccess = true
	bc.ChatListWait = 30
	bc.PortalSyncWait = 600

	bc.CallNotices.Start = true
	bc.CallNotices.End = true

	bc.InitialChatSync = 10
	bc.InitialHistoryFill = 20
	bc.RecoverChatSync = -1
	bc.RecoverHistory = true
	bc.SyncChatMaxAge = 259200
	bc.SyncContact = false

	bc.SyncWithCustomPuppets = true

	bc.InviteOwnPuppetForBackfilling = true
	bc.PrivateChatPortalMeta = false
}

type umBridgeConfig BridgeConfig

func (bc *BridgeConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	err := unmarshal((*umBridgeConfig)(bc))
	if err != nil {
		return err
	}

	bc.usernameTemplate, err = template.New("username").Parse(bc.UsernameTemplate)
	if err != nil {
		return err
	}

	bc.displaynameTemplate, err = template.New("displayname").Parse(bc.DisplaynameTemplate)
	if err != nil {
		return err
	}

	if len(bc.CommunityTemplate) > 0 {
		bc.communityTemplate, err = template.New("community").Parse(bc.CommunityTemplate)
		if err != nil {
			return err
		}
	}

	return nil
}

type UsernameTemplateArgs struct {
	UserID id.UserID
}

func (bc BridgeConfig) FormatDisplayname(contact skype.Contact) (string, int8) {
	var buf bytes.Buffer
	if index := strings.IndexRune(contact.PersonId, '@'); index > 0 {
		contact.PersonId = "+" + contact.PersonId[:index]
	}
	bc.displaynameTemplate.Execute(&buf, contact)
	var quality int8
	switch {
	//case len(contact.Notify) > 0:
	//	quality = 3
	case len(contact.DisplayName) > 0:
		quality = 3
	case len(contact.PersonId) > 0:
		quality = 1
	default:
		quality = 0
	}
	return buf.String(), quality
}

func (bc BridgeConfig) FormatUsername(userID types.SkypeID) string {
	var buf bytes.Buffer
	bc.usernameTemplate.Execute(&buf, userID)
	return buf.String()
}

type CommunityTemplateArgs struct {
	Localpart string
	Server    string
}

func (bc BridgeConfig) EnableCommunities() bool {
	return bc.communityTemplate != nil
}

func (bc BridgeConfig) FormatCommunity(localpart, server string) string {
	var buf bytes.Buffer
	bc.communityTemplate.Execute(&buf, CommunityTemplateArgs{localpart, server})
	return buf.String()
}

type PermissionConfig map[string]PermissionLevel

type PermissionLevel int

const (
	PermissionLevelDefault  PermissionLevel = 0
	PermissionLevelRelaybot PermissionLevel = 5
	PermissionLevelUser     PermissionLevel = 10
	PermissionLevelAdmin    PermissionLevel = 100
)

func (pc *PermissionConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	rawPC := make(map[string]string)
	err := unmarshal(&rawPC)
	if err != nil {
		return err
	}

	if *pc == nil {
		*pc = make(map[string]PermissionLevel)
	}
	for key, value := range rawPC {
		switch strings.ToLower(value) {
		case "relaybot":
			(*pc)[key] = PermissionLevelRelaybot
		case "user":
			(*pc)[key] = PermissionLevelUser
		case "admin":
			(*pc)[key] = PermissionLevelAdmin
		default:
			val, err := strconv.Atoi(value)
			if err != nil {
				(*pc)[key] = PermissionLevelDefault
			} else {
				(*pc)[key] = PermissionLevel(val)
			}
		}
	}
	return nil
}

func (pc *PermissionConfig) MarshalYAML() (interface{}, error) {
	if *pc == nil {
		return nil, nil
	}
	rawPC := make(map[string]string)
	for key, value := range *pc {
		switch value {
		case PermissionLevelRelaybot:
			rawPC[key] = "relaybot"
		case PermissionLevelUser:
			rawPC[key] = "user"
		case PermissionLevelAdmin:
			rawPC[key] = "admin"
		default:
			rawPC[key] = strconv.Itoa(int(value))
		}
	}
	return rawPC, nil
}

func (pc PermissionConfig) IsRelaybotWhitelisted(userID id.UserID) bool {
	return pc.GetPermissionLevel(userID) >= PermissionLevelRelaybot
}

func (pc PermissionConfig) IsWhitelisted(userID id.UserID) bool {
	return pc.GetPermissionLevel(userID) >= PermissionLevelUser
}

func (pc PermissionConfig) IsAdmin(userID id.UserID) bool {
	return pc.GetPermissionLevel(userID) >= PermissionLevelAdmin
}

func (pc PermissionConfig) GetPermissionLevel(userID id.UserID) PermissionLevel {
	permissions, ok := pc[string(userID)]
	if ok {
		return permissions
	}

	_, homeserver, _ := userID.Parse()
	permissions, ok = pc[homeserver]
	if len(homeserver) > 0 && ok {
		return permissions
	}

	permissions, ok = pc["*"]
	if ok {
		return permissions
	}

	return PermissionLevelDefault
}

type RelaybotConfig struct {
	Enabled        bool        `yaml:"enabled"`
	ManagementRoom id.RoomID   `yaml:"management"`
	InviteUsers    []id.UserID `yaml:"invites"`

	MessageFormats   map[event.MessageType]string `yaml:"message_formats"`
	messageTemplates *template.Template           `yaml:"-"`
}

type umRelaybotConfig RelaybotConfig

func (rc *RelaybotConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	err := unmarshal((*umRelaybotConfig)(rc))
	if err != nil {
		return err
	}

	rc.messageTemplates = template.New("messageTemplates")
	for key, format := range rc.MessageFormats {
		_, err := rc.messageTemplates.New(string(key)).Parse(format)
		if err != nil {
			return err
		}
	}

	return nil
}

type Sender struct {
	UserID id.UserID
	*event.MemberEventContent
}

type formatData struct {
	Sender  Sender
	Message string
	Content *event.MessageEventContent
}

func (rc *RelaybotConfig) FormatMessage(content *event.MessageEventContent, sender id.UserID, member *event.MemberEventContent) (string, error) {
	var output strings.Builder
	err := rc.messageTemplates.ExecuteTemplate(&output, string(content.MsgType), formatData{
		Sender: Sender{
			UserID:             sender,
			MemberEventContent: member,
		},
		Content: content,
		Message: content.FormattedBody,
	})
	return output.String(), err
}
