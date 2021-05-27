package main

import (
	"bytes"
	"github.com/gabriel-vasile/mimetype"
	"maunium.net/go/mautrix/patch"

	"encoding/hex"
	"encoding/xml"
	"fmt"
	skype "github.com/kelaresg/go-skypeapi"
	skypeExt "github.com/kelaresg/matrix-skype/skype-ext"
	"html"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	log "maunium.net/go/maulogger/v2"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
	"maunium.net/go/mautrix/pushrules"

	"github.com/kelaresg/matrix-skype/database"
	"github.com/kelaresg/matrix-skype/types"
)

func (bridge *Bridge) GetPortalByMXID(mxid id.RoomID) *Portal {
	bridge.portalsLock.Lock()
	defer bridge.portalsLock.Unlock()
	portal, ok := bridge.portalsByMXID[mxid]
	if !ok {
		fmt.Println("loadDBPortal1")
		return bridge.loadDBPortal(bridge.DB.Portal.GetByMXID(mxid), nil)
	}
	return portal
}

func (bridge *Bridge) GetPortalByJID(key database.PortalKey) *Portal {
	bridge.portalsLock.Lock()
	defer bridge.portalsLock.Unlock()
	portal, ok := bridge.portalsByJID[key]
	if !ok {
		fmt.Println("loadDBPortal2")
		return bridge.loadDBPortal(bridge.DB.Portal.GetByJID(key), &key)
	}
	return portal
}

func (bridge *Bridge) GetAllPortals() []*Portal {
	return bridge.dbPortalsToPortals(bridge.DB.Portal.GetAll())
}

func (bridge *Bridge) GetAllPortalsByJID(jid types.SkypeID) []*Portal {
	return bridge.dbPortalsToPortals(bridge.DB.Portal.GetAllByJID(jid))
}

func (bridge *Bridge) dbPortalsToPortals(dbPortals []*database.Portal) []*Portal {
	bridge.portalsLock.Lock()
	defer bridge.portalsLock.Unlock()
	output := make([]*Portal, len(dbPortals))
	for index, dbPortal := range dbPortals {
		if dbPortal == nil {
			continue
		}
		portal, ok := bridge.portalsByJID[dbPortal.Key]
		if !ok {
			fmt.Println("loadDBPortal3")
			portal = bridge.loadDBPortal(dbPortal, nil)
		}
		output[index] = portal
	}
	return output
}

func (bridge *Bridge) loadDBPortal(dbPortal *database.Portal, key *database.PortalKey) *Portal {
	fmt.Println("loadDBPortal: ", dbPortal)
	if dbPortal == nil {
		if key == nil {
			return nil
		}
		dbPortal = bridge.DB.Portal.New()
		dbPortal.Key = *key
		dbPortal.Insert()
	}
	portal := bridge.NewPortal(dbPortal)
	bridge.portalsByJID[portal.Key] = portal
	fmt.Println("loadDBPortal portal.MXID", portal.MXID)
	if len(portal.MXID) > 0 {
		bridge.portalsByMXID[portal.MXID] = portal
	}
	return portal
}

func (portal *Portal) GetUsers() []*User {
	return nil
}

func (bridge *Bridge) NewPortal(dbPortal *database.Portal) *Portal {
	portal := &Portal{
		Portal: dbPortal,
		bridge: bridge,
		log:    bridge.Log.Sub(fmt.Sprintf("Portal/%s", dbPortal.Key)),

		recentlyHandled: [recentlyHandledLength]types.SkypeMessageID{},

		messages: make(chan PortalMessage, 128),
	}
	fmt.Println("NewPortal: ")
	go portal.handleMessageLoop()
	return portal
}

const recentlyHandledLength = 100

type PortalMessage struct {
	chat      string
	source    *User
	data      interface{}
	timestamp uint64
}

type Portal struct {
	*database.Portal

	bridge *Bridge
	log    log.Logger

	roomCreateLock sync.Mutex

	recentlyHandled      [recentlyHandledLength]types.SkypeMessageID
	recentlyHandledLock  sync.Mutex
	recentlyHandledIndex uint8

	backfillLock  sync.Mutex
	backfilling   bool
	lastMessageTs uint64

	privateChatBackfillInvitePuppet func()

	messages chan PortalMessage

	isPrivate   *bool
	hasRelaybot *bool
}

const MaxMessageAgeToCreatePortal = 5 * 60 // 5 minutes

func (portal *Portal) handleMessageLoop() {
	for msg := range portal.messages {
		fmt.Println()
		fmt.Printf("portal handleMessageLoop: %+v", msg)
		if len(portal.MXID) == 0 {
			if msg.timestamp+MaxMessageAgeToCreatePortal < uint64(time.Now().Unix()) {
				portal.log.Debugln("Not creating portal room for incoming message as the message is too old.")
				continue
			}
			portal.log.Debugln("Creating Matrix room from incoming message")
			err := portal.CreateMatrixRoom(msg.source)
			if err != nil {
				portal.log.Errorln("Failed to create portal room:", err)
				fmt.Println()
				fmt.Printf("portal handleMessageLoop2: %+v", msg)
				return
			}
		}
		fmt.Println()
		fmt.Printf("portal handleMessageLoop3: %+v", msg)
		portal.backfillLock.Lock()
		portal.handleMessage(msg)
		portal.backfillLock.Unlock()
	}
}

func (portal *Portal) handleMessage(msg PortalMessage) {
	fmt.Println()
	fmt.Printf("portal handleMessage: %+v", msg)
	if len(portal.MXID) == 0 {
		portal.log.Warnln("handleMessage called even though portal.MXID is empty")
		return
	}

	data, ok := msg.data.(skype.Resource)
	if ok {
		switch data.MessageType {
		case "RichText", "Text":
			portal.HandleTextMessage(msg.source, data)
		case "RichText/UriObject":
			//portal.HandleMediaMessage(msg.source, data.Download, data.Thumbnail, data.Info, data.ContextInfo, data.Type, data.Caption, 0, false)
			portal.HandleMediaMessageSkype(msg.source, data.Download, data.MessageType,nil, data,false)
		case "RichText/Media_Video":
			//portal.HandleMediaMessage(msg.source, data.Download, data.Thumbnail, data.Info, data.ContextInfo, data.Type, data.Caption, 0, false)
			portal.HandleMediaMessageSkype(msg.source, data.Download, data.MessageType,nil, data,false)
		case "RichText/Media_AudioMsg":
			//portal.HandleMediaMessage(msg.source, data.Download, data.Thumbnail, data.Info, data.ContextInfo, data.Type, data.Caption, 0, false)
			portal.HandleMediaMessageSkype(msg.source, data.Download, data.MessageType,nil, data,false)
		case "RichText/Media_GenericFile":
			//portal.HandleMediaMessage(msg.source, data.Download, data.Thumbnail, data.Info, data.ContextInfo, data.Type, data.Caption, 0, false)
			portal.HandleMediaMessageSkype(msg.source, data.Download, data.MessageType,nil, data,false)
		case "RichText/Contacts":
			portal.HandleContactMessageSkype(msg.source, data)
		case "RichText/Location":
			portal.HandleLocationMessageSkype(msg.source, data)
		default:
			portal.log.Warnln("Unknown message type:", reflect.TypeOf(msg.data))
		}
	} else {
		portal.log.Warnln("Unknown message type:", reflect.TypeOf(msg.data))
	}
}

func (portal *Portal) isRecentlyHandled(id types.SkypeMessageID) bool {
	start := portal.recentlyHandledIndex
	for i := start; i != start; i = (i - 1) % recentlyHandledLength {
		if portal.recentlyHandled[i] == id {
			return true
		}
	}
	return false
}

func (portal *Portal) isDuplicate(clientMessageId types.SkypeMessageID, id string) bool {
	msg := portal.bridge.DB.Message.GetByJID(portal.Key, clientMessageId)
	if msg != nil && len(msg.ID) < 1 {
		msg.UpdateIDByJID(id)
	}
	if msg != nil {
		return true
	}
	return false
}

//func init() {
//	gob.Register(&waProto.Message{})
//}

//func (portal *Portal) markHandled(source *User, message *waProto.WebMessageInfo, mxid id.EventID) {
//	msg := portal.bridge.DB.Message.New()
//	msg.Chat = portal.Key
//	msg.JID = message.GetKey().GetId()
//	msg.MXID = mxid
//	msg.Timestamp = message.GetMessageTimestamp()
//	if message.GetKey().GetFromMe() {
//		msg.Sender = source.JID
//	} else if portal.IsPrivateChat() {
//		msg.Sender = portal.Key.JID
//	} else {
//		msg.Sender = message.GetKey().GetParticipant()
//		if len(msg.Sender) == 0 {
//			msg.Sender = message.GetParticipant()
//		}
//	}
//	//msg.Content = message.Message
//	msg.Content = &skype.Resource{}
//	msg.Insert()
//
//	portal.recentlyHandledLock.Lock()
//	index := portal.recentlyHandledIndex
//	portal.recentlyHandledIndex = (portal.recentlyHandledIndex + 1) % recentlyHandledLength
//	portal.recentlyHandledLock.Unlock()
//	portal.recentlyHandled[index] = msg.JID
//}

func (portal *Portal) markHandledSkype(source *User, message *skype.Resource, mxid id.EventID) {
	msg := portal.bridge.DB.Message.New()
	msg.Chat = portal.Key
	msg.JID = message.ClientMessageId
	msg.MXID = mxid
	msg.Timestamp = uint64(message.Timestamp)
	if message.GetFromMe(source.Conn.Conn) {
		msg.Sender = source.JID
	} else if portal.IsPrivateChat() {
		msg.Sender = portal.Key.JID
	} else {
		msg.Sender = source.JID
		//if len(msg.Sender) == 0 {
		//	msg.Sender = message.Jid
		//}
	}

	msg.Content = message.Content
	if len(message.Id)>0 {
		msg.ID = message.Id
	}
	msg.Insert()
	fmt.Println("markHandledSkype1", msg.Chat.JID)
	fmt.Println("markHandledSkype2", msg.JID)
	portal.recentlyHandledLock.Lock()
	index := portal.recentlyHandledIndex
	portal.recentlyHandledIndex = (portal.recentlyHandledIndex + 1) % recentlyHandledLength
	portal.recentlyHandledLock.Unlock()
	portal.recentlyHandled[index] = msg.JID
}

//func (portal *Portal) getMessageIntent(user *User, info whatsapp.MessageInfo) *appservice.IntentAPI {
//	if info.FromMe {
//		return portal.bridge.GetPuppetByJID(user.JID).IntentFor(portal)
//	} else if portal.IsPrivateChat() {
//		return portal.MainIntent()
//	} else if len(info.SenderJid) == 0 {
//		if len(info.Source.GetParticipant()) != 0 {
//			info.SenderJid = info.Source.GetParticipant()
//		} else {
//			return nil
//		}
//	}
//	return portal.bridge.GetPuppetByJID(info.SenderJid).IntentFor(portal)
//}

func (portal *Portal) getMessageIntentSkype(user *User, info skype.Resource) *appservice.IntentAPI {
	if info.GetFromMe(user.Conn.Conn) {
		return portal.bridge.GetPuppetByJID(user.JID).IntentFor(portal)
	} else if portal.IsPrivateChat() {
		return portal.MainIntent()
	} else if len(info.SendId) == 0 {
		//if len(info.Source.GetParticipant()) != 0 {
		//	info.SenderJid = info.Source.GetParticipant()
		//} else {
		//	return nil
		//}
		return nil
	}
	fmt.Println()
	fmt.Println("getMessageIntentSkype")
	fmt.Println()
	return portal.bridge.GetPuppetByJID(info.SendId+skypeExt.NewUserSuffix).IntentFor(portal)
}

func (portal *Portal) handlePrivateChatFromMe(fromMe bool) func() {
	if portal.IsPrivateChat() && fromMe && len(portal.bridge.Config.Bridge.LoginSharedSecret) == 0 {
		var privateChatPuppet *Puppet
		var privateChatPuppetInvited bool
		privateChatPuppet = portal.bridge.GetPuppetByJID(portal.Key.Receiver)
		if privateChatPuppetInvited {
			return nil
		}
		privateChatPuppetInvited = true
		_, _ = portal.MainIntent().InviteUser(portal.MXID, &mautrix.ReqInviteUser{UserID: privateChatPuppet.MXID})
		_ = privateChatPuppet.DefaultIntent().EnsureJoined(portal.MXID)

		return func() {
			if privateChatPuppet != nil && privateChatPuppetInvited {
				//_, _ = privateChatPuppet.DefaultIntent().LeaveRoom(portal.MXID)
			}
		}
	}
	return nil
}

func (portal *Portal) startHandlingSkype(source *User, info skype.Resource) (*appservice.IntentAPI, func()) {
	// TODO these should all be trace logs
	if portal.lastMessageTs > uint64(info.Timestamp)+1 {
		portal.log.Debugfln("Not handling %s: message is older (%d) than last bridge message (%d)", info.Id, info.Timestamp, portal.lastMessageTs)
	} else if portal.isRecentlyHandled(info.Id) {
		portal.log.Debugfln("Not handling %s: message was recently handled", info.Id)
	} else if portal.isDuplicate(info.ClientMessageId, info.Id) {
		portal.log.Debugfln("Not handling %s: message is duplicate", info.ClientMessageId)
	} else {
		portal.log.Debugfln("Starting handling of %s (ts: %d)", info.Id, info.Timestamp)
		portal.lastMessageTs = uint64(info.Timestamp)
		return portal.getMessageIntentSkype(source, info), portal.handlePrivateChatFromMe(info.GetFromMe(source.Conn.Conn))
	}
	fmt.Println()
	fmt.Printf("portal startHandling: %+v", "but nil")
	return nil, nil
}

//func (portal *Portal) finishHandling(source *User, message *waProto.WebMessageInfo, mxid id.EventID) {
//	portal.markHandled(source, message, mxid)
//	portal.sendDeliveryReceipt(mxid)
//	portal.log.Debugln("Handled message", message.GetKey().GetId(), "->", mxid)
//}

func (portal *Portal) finishHandlingSkype(source *User, message *skype.Resource, mxid id.EventID) {
	portal.markHandledSkype(source, message, mxid)
	portal.sendDeliveryReceipt(mxid)
	portal.log.Debugln("Handled message", message.Jid, "->", mxid)
}

func (portal *Portal) SyncParticipants(metadata *skypeExt.GroupInfo) {
	changed := false
	fmt.Println("SyncParticipants: 0")
	levels, err := portal.MainIntent().PowerLevels(portal.MXID)
	if err != nil {
		fmt.Println("SyncParticipants: 1")
		levels = portal.GetBasePowerLevels()
		changed = true
	}
	for _, participant := range metadata.Participants {
		fmt.Println("SyncParticipants: participant.JID= ", participant.JID)
		user := portal.bridge.GetUserByJID(participant.JID)
		portal.userMXIDAction(user, portal.ensureMXIDInvited)

		puppet := portal.bridge.GetPuppetByJID(participant.JID)
		fmt.Println("SyncParticipants: portal.MXID = ", portal.MXID)
		err := puppet.IntentFor(portal).EnsureJoined(portal.MXID)
		if err != nil {
			portal.log.Warnfln("Failed to make puppet of %s join %s: %v", participant.JID, portal.MXID, err)
		}

		expectedLevel := 0
		if participant.IsSuperAdmin {
			expectedLevel = 95
		} else if participant.IsAdmin {
			expectedLevel = 50
		}
		changed = levels.EnsureUserLevel(puppet.MXID, expectedLevel) || changed
		if user != nil {
			changed = levels.EnsureUserLevel(user.MXID, expectedLevel) || changed
		}
	}
	if changed {
		_, err = portal.MainIntent().SetPowerLevels(portal.MXID, levels)
		if err != nil {
			portal.log.Errorln("Failed to change power levels1:", err)
		}
	}
}

func (portal *Portal) UpdateAvatar(user *User, avatar *skypeExt.ProfilePicInfo) bool {
	if avatar == nil || strings.Count(avatar.URL, "")-1 < 1 {
		//var err error
		//avatar, err = user.Conn.GetProfilePicThumb(portal.Key.JID)
		//if err != nil {
		//	portal.log.Errorln(err)
		//	return false
		//}
		return false
	}
	avatar.Authorization = "skype_token " + user.Conn.LoginInfo.SkypeToken
	if avatar.Status != 0 {
		return false
	}

	if portal.Avatar == avatar.Tag {
		return false
	}

	data, err := avatar.DownloadBytes()

	if err != nil {
		portal.log.Warnln("Failed to download avatar:", err)
		return false
	}

	mimeType := http.DetectContentType(data)
	resp, err := portal.MainIntent().UploadBytes(data, mimeType)
	if err != nil {
		portal.log.Warnln("Failed to upload avatar:", err)
		return false
	}

	portal.AvatarURL = resp.ContentURI
	if len(portal.MXID) > 0 {
		_, err = portal.MainIntent().SetRoomAvatar(portal.MXID, resp.ContentURI)
		if err != nil {
			portal.log.Warnln("Failed to set room topic:", err)
			return false
		}
	}
	portal.Avatar = avatar.Tag
	return true
}

func (portal *Portal) UpdateName(name string, setBy types.SkypeID) bool {
	if portal.Name != name {
		intent := portal.MainIntent()
		if len(setBy) > 0 {
			intent = portal.bridge.GetPuppetByJID(setBy).IntentFor(portal)
		}
		_, err := intent.SetRoomName(portal.MXID, name)
		if err == nil {
			portal.Name = name
			return true
		}
		portal.log.Warnln("Failed to set room name:", err)
	}
	return false
}

func (portal *Portal) UpdateTopic(topic string, setBy types.SkypeID) bool {
	if portal.Topic != topic {
		intent := portal.MainIntent()
		if len(setBy) > 0 {
			intent = portal.bridge.GetPuppetByJID(setBy).IntentFor(portal)
		}
		_, err := intent.SetRoomTopic(portal.MXID, topic)
		if err == nil {
			portal.Topic = topic
			return true
		}
		portal.log.Warnln("Failed to set room topic:", err)
	}
	return false
}

func (portal *Portal) UpdateMetadata(user *User) bool {
	if portal.IsPrivateChat() {
		return false
	} else if portal.IsStatusBroadcastRoom() {
		update := false
		update = portal.UpdateName("skype Status Broadcast", "") || update
		update = portal.UpdateTopic("skype status updates from your contacts", "") || update
		return update
	}
	metadata, err := user.Conn.GetGroupMetaData(portal.Key.JID)
	if err != nil {
		portal.log.Errorln(err)
		fmt.Println()
		fmt.Println("UpdateMetadata0: ", err)
		fmt.Println()
		return false
	}

	portalName := ""
	noRoomTopic := false
	names := strings.Split(metadata.Name, ", ")
	for _, name := range names {
		key := "8:" + name + skypeExt.NewUserSuffix
		if key == user.JID {
			noRoomTopic = true
		}
	}
	if noRoomTopic {
		for index, participant := range metadata.Participants {
			fmt.Println()
			fmt.Printf("metadata.Participants1: %+v", participant)
			fmt.Println()

			if participant.JID == user.JID {
				continue
			}
			if contact, ok := user.Conn.Store.Contacts[participant.JID]; ok {
				if len(portalName) == 0 {
					portalName = contact.DisplayName
				} else {
					if index > 5 {
						portalName = portalName + ", ..."
						break
					} else {
						portalName = portalName + ", " + contact.DisplayName
					}
				}
			}
		}
	} else {
		portalName = metadata.Name
	}
	// portal.Topic = ""
	//if metadata.Status != 0 {
		// 401: access denied
		// 404: group does (no longer) exist
		// 500: ??? happens with status@broadcast

		// TODO: update the room, e.g. change priority level
		//   to send messages to moderator
	//	return false
	//}

	portal.SyncParticipants(metadata)
	update := false
	update = portal.UpdateName(portalName, metadata.NameSetBy) || update
	// update = portal.UpdateTopic(metadata.Topic, metadata.TopicSetBy) || update
	return update
}

func (portal *Portal) userMXIDAction(user *User, fn func(mxid id.UserID)) {
	if user == nil {
		return
	}

	if user == portal.bridge.Relaybot {
		for _, mxid := range portal.bridge.Config.Bridge.Relaybot.InviteUsers {
			fn(mxid)
		}
	} else {
		fn(user.MXID)
	}
}

func (portal *Portal) ensureMXIDInvited(mxid id.UserID) {
	err := portal.MainIntent().EnsureInvited(portal.MXID, mxid)
	if err != nil {
		portal.log.Warnfln("Failed to ensure %s is invited to %s: %v", mxid, portal.MXID, err)
	}
}

func (portal *Portal) ensureUserInvited(user *User) {
	portal.userMXIDAction(user, portal.ensureMXIDInvited)

	customPuppet := portal.bridge.GetPuppetByCustomMXID(user.MXID)
	if customPuppet != nil && customPuppet.CustomIntent() != nil {
		_ = customPuppet.CustomIntent().EnsureJoined(portal.MXID)
	}
}

func (portal *Portal) SyncSkype(user *User, chat skype.Conversation) {
	portal.log.Infoln("Syncing portal for", user.MXID)

	if user.IsRelaybot {
		yes := true
		portal.hasRelaybot = &yes
	}

	newPortal := false
	if len(portal.MXID) == 0 {
		if !portal.IsPrivateChat() {
			portal.Name = chat.ThreadProperties.Topic
		}
		//todo
		fmt.Println("SyncSkype portal.MXID", portal.MXID)
		err := portal.CreateMatrixRoom(user)
		if err != nil {
			portal.log.Errorln("Failed to create portal room:", err)
			return
		}
		newPortal = true
	} else {
		fmt.Println("SyncSkype ensureUserInvited", portal.MXID)
		portal.ensureUserInvited(user)
		//rep, err := portal.MainIntent().SetPowerLevel(portal.MXID, user.MXID, 95)
		//if err != nil {
		//	portal.log.Warnfln("SyncSkype: SetPowerLevel err: ", err, rep)
		//}

		//if portal.IsPrivateChat() {
		//	preUserIds,_ :=  portal.GetMatrixUsers()
		//	for _,userId := range preUserIds {
		//		if user.MXID != userId {
		//			err := portal.tryKickUser(userId, portal.MainIntent())
		//			if err != nil {
		//				portal.log.Errorln("Failed to try kick user:", err)
		//			}
		//		}
		//	}
		//}
	}

	if portal.IsPrivateChat() {
		return
	}

	fmt.Println("SyncSkype portal")

	update := false
	if !newPortal {
		update = portal.UpdateMetadata(user) || update
	}
	// if !portal.IsStatusBroadcastRoom() {
		//fmt.Println("SyncSkype portal.UpdateAvatar", portal.MXID)
		// update = portal.UpdateAvatar(user, nil) || update
	// }
	if update {
		fmt.Println("SyncSkype portal.Update", portal.MXID)
		portal.Update()
	}
}

//func (portal *Portal) Sync(user *User, contact whatsapp.Contact) {
//	portal.log.Infoln("Syncing portal for", user.MXID)
//
//	if user.IsRelaybot {
//		yes := true
//		portal.hasRelaybot = &yes
//	}
//
//	if len(portal.MXID) == 0 {
//		if !portal.IsPrivateChat() {
//			portal.Name = contact.Name
//		}
//		err := portal.CreateMatrixRoom(user)
//		if err != nil {
//			portal.log.Errorln("Failed to create portal room:", err)
//			return
//		}
//	} else {
//		portal.ensureUserInvited(user)
//	}
//
//	if portal.IsPrivateChat() {
//		return
//	}
//
//	update := false
//	update = portal.UpdateMetadata(user) || update
//	if !portal.IsStatusBroadcastRoom() {
//		update = portal.UpdateAvatar(user, nil) || update
//	}
//	if update {
//		portal.Update()
//	}
//}

func (portal *Portal) GetBasePowerLevels() *event.PowerLevelsEventContent {
	anyone := 0
	nope := 95
	invite := 50
	if portal.bridge.Config.Bridge.AllowUserInvite {
		invite = 0
	}
	return &event.PowerLevelsEventContent{
		UsersDefault:    anyone,
		EventsDefault:   anyone,
		RedactPtr:       &anyone,
		StateDefaultPtr: &nope,
		BanPtr:          &nope,
		InvitePtr:       &invite,
		Users: map[id.UserID]int{
			portal.MainIntent().UserID: 100,
		},
		Events: map[string]int{
			event.StateRoomName.Type:   anyone,
			event.StateRoomAvatar.Type: anyone,
			event.StateTopic.Type:      anyone,
		},
	}
}

func (portal *Portal) ChangeAdminStatus(jids []string, setAdmin bool) {
	levels, err := portal.MainIntent().PowerLevels(portal.MXID)
	if err != nil {
		levels = portal.GetBasePowerLevels()
	}
	newLevel := 0
	if setAdmin {
		newLevel = 50
	}
	changed := false
	for _, jid := range jids {
		puppet := portal.bridge.GetPuppetByJID(jid)
		changed = levels.EnsureUserLevel(puppet.MXID, newLevel) || changed

		user := portal.bridge.GetUserByJID(jid)
		if user != nil {
			changed = levels.EnsureUserLevel(user.MXID, newLevel) || changed
		}
	}
	if changed {
		_, err = portal.MainIntent().SetPowerLevels(portal.MXID, levels)
		if err != nil {
			portal.log.Errorln("Failed to change power levels2:", err)
		}
	}
}

//func (portal *Portal) membershipRemove(jids []string, action skypeExt.ChatActionType) {
//	for _, jid := range jids {
//		jidArr := strings.Split(jid, "@c.")
//		jid = jidArr[0]
//		member := portal.bridge.GetPuppetByJID(jid)
//		if member == nil {
//			portal.log.Errorln("%s is not exist", jid)
//			continue
//		}
//		_, err := portal.MainIntent().KickUser(portal.MXID, &mautrix.ReqKickUser{
//			UserID: member.MXID,
//		})
//		if err != nil {
//			portal.log.Errorln("Error %s member from skype: %v", action, err)
//		}
//	}
//}

func (portal *Portal) membershipRemove(content string) {
	xmlFormat := skype.XmlDeleteMember{}
	err := xml.Unmarshal([]byte(content), &xmlFormat)
	for _, target := range xmlFormat.Targets {
		member := portal.bridge.GetPuppetByJID(target)
		memberMXID := id.UserID(patch.Parse(string(member.MXID)))
		if portal.bridge.AS.StateStore.IsInRoom(portal.MXID, memberMXID) {
			_, err = portal.MainIntent().KickUser(portal.MXID, &mautrix.ReqKickUser{
				UserID: member.MXID,
			})
			if err != nil {
				portal.log.Errorln("Error kick member from matrix after kick from skype: %v", err)
			}
		}
	}
}

func (portal *Portal) membershipAdd(content string) {
	xmlFormat := skype.XmlAddMember{}
	err := xml.Unmarshal([]byte(content), &xmlFormat)

	for _, target := range xmlFormat.Targets {
		puppet := portal.bridge.GetPuppetByJID(target)
		fmt.Println("membershipAdd puppet jid", target)
		err = puppet.IntentFor(portal).EnsureJoined(portal.MXID)
		if err != nil {
			portal.log.Errorln("Error %v joined member from skype:", err)
		}
	}
}

func (portal *Portal) membershipCreate(user *User, cmd skypeExt.ChatUpdate) {
	//contact := skype.Contact{
	//	Jid:    cmd.Data.SenderJID,
	//	Notify: "",
	//	Name:   cmd.Data.Create.Name,
	//	Short:  "",
	//}
	//portal.Sync(user, contact)
	//contact.Jid = cmd.JID
	//user.Conn.Store.Contacts[cmd.JID] = contact
}

func (portal *Portal) RestrictMessageSending(restrict bool) {
	levels, err := portal.MainIntent().PowerLevels(portal.MXID)
	if err != nil {
		levels = portal.GetBasePowerLevels()
	}
	if restrict {
		levels.EventsDefault = 50
	} else {
		levels.EventsDefault = 0
	}
	_, err = portal.MainIntent().SetPowerLevels(portal.MXID, levels)
	if err != nil {
		portal.log.Errorln("Failed to change power levels3:", err)
	}
}

func (portal *Portal) RestrictMetadataChanges(restrict bool) {
	levels, err := portal.MainIntent().PowerLevels(portal.MXID)
	if err != nil {
		levels = portal.GetBasePowerLevels()
	}
	newLevel := 0
	if restrict {
		newLevel = 50
	}
	changed := false
	changed = levels.EnsureEventLevel(event.StateRoomName, newLevel) || changed
	changed = levels.EnsureEventLevel(event.StateRoomAvatar, newLevel) || changed
	changed = levels.EnsureEventLevel(event.StateTopic, newLevel) || changed
	if changed {
		_, err = portal.MainIntent().SetPowerLevels(portal.MXID, levels)
		if err != nil {
			portal.log.Errorln("Failed to change power levels4:", err)
		}
	}
}

func (portal *Portal) BackfillHistory(user *User, lastMessageTime uint64) error {
	if !portal.bridge.Config.Bridge.RecoverHistory {
		return nil
	}

	endBackfill := portal.beginBackfill()
	defer endBackfill()

	lastMessage := portal.bridge.DB.Message.GetLastInChat(portal.Key)
	if lastMessage == nil {
		return nil
	}
	if lastMessage.Timestamp >= lastMessageTime {
		portal.log.Debugln("Not backfilling: no new messages")
		return nil
	}

	lastMessageID := lastMessage.JID
	//lastMessageFromMe := lastMessage.Sender == user.JID
	portal.log.Infoln("Backfilling history since", lastMessageID, "for", user.MXID)
	for len(lastMessageID) > 0 {
		portal.log.Debugln("Backfilling history: 50 messages after", lastMessageID)
		//resp, err := user.Conn.LoadMessagesAfter(portal.Key.JID, lastMessageID, lastMessageFromMe, 50)
		//if err != nil {
		//	return err
		//}
		//messages, ok := resp.Content.([]interface{})
		//if !ok || len(messages) == 0 {
		//	break
		//}

		//portal.handleHistory(user, messages)
		//
		//lastMessageProto, ok := messages[len(messages)-1].(*waProto.WebMessageInfo)
		//if ok {
		//	lastMessageID = lastMessageProto.GetKey().GetId()
		//	lastMessageFromMe = lastMessageProto.GetKey().GetFromMe()
		//}
	}
	portal.log.Infoln("Backfilling finished")
	return nil
}

func (portal *Portal) beginBackfill() func() {
	portal.backfillLock.Lock()
	portal.backfilling = true
	var privateChatPuppetInvited bool
	var privateChatPuppet *Puppet
	if portal.IsPrivateChat() && portal.bridge.Config.Bridge.InviteOwnPuppetForBackfilling {
		receiverId := portal.Key.Receiver
		if strings.Index(receiverId, skypeExt.NewUserSuffix) > 0 {
			receiverId = strings.ReplaceAll(receiverId, skypeExt.NewUserSuffix, "")
		}
		privateChatPuppet = portal.bridge.GetPuppetByJID(receiverId)
		portal.privateChatBackfillInvitePuppet = func() {
			if privateChatPuppetInvited {
				return
			}
			privateChatPuppetInvited = true
			_, _ = portal.MainIntent().InviteUser(portal.MXID, &mautrix.ReqInviteUser{UserID: privateChatPuppet.MXID})
			_ = privateChatPuppet.DefaultIntent().EnsureJoined(portal.MXID)
		}
	}
	return func() {
		portal.backfilling = false
		portal.privateChatBackfillInvitePuppet = nil
		portal.backfillLock.Unlock()
		if privateChatPuppet != nil && privateChatPuppetInvited {
			if len(portal.bridge.Config.Bridge.LoginSharedSecret) > 0 {
				_, _ = privateChatPuppet.DefaultIntent().LeaveRoom(portal.MXID)
			}
		}
	}
}

func (portal *Portal) disableNotifications(user *User) {
	if !portal.bridge.Config.Bridge.HistoryDisableNotifs {
		return
	}
	puppet := portal.bridge.GetPuppetByCustomMXID(user.MXID)
	if puppet == nil || puppet.customIntent == nil {
		return
	}
	portal.log.Debugfln("Disabling notifications for %s for backfilling", user.MXID)
	ruleID := fmt.Sprintf("net.maunium.silence_while_backfilling.%s", portal.MXID)
	err := puppet.customIntent.PutPushRule("global", pushrules.OverrideRule, ruleID, &mautrix.ReqPutPushRule{
		Actions: []pushrules.PushActionType{pushrules.ActionDontNotify},
		Conditions: []pushrules.PushCondition{{
			Kind:    pushrules.KindEventMatch,
			Key:     "room_id",
			Pattern: string(portal.MXID),
		}},
	})
	if err != nil {
		portal.log.Warnfln("Failed to disable notifications for %s while backfilling: %v", user.MXID, err)
	}
}

func (portal *Portal) enableNotifications(user *User) {
	if !portal.bridge.Config.Bridge.HistoryDisableNotifs {
		return
	}
	puppet := portal.bridge.GetPuppetByCustomMXID(user.MXID)
	if puppet == nil || puppet.customIntent == nil {
		return
	}
	portal.log.Debugfln("Re-enabling notifications for %s after backfilling", user.MXID)
	ruleID := fmt.Sprintf("net.maunium.silence_while_backfilling.%s", portal.MXID)
	err := puppet.customIntent.DeletePushRule("global", pushrules.OverrideRule, ruleID)
	if err != nil {
		portal.log.Warnfln("Failed to re-enable notifications for %s after backfilling: %v", user.MXID, err)
	}
}

func (portal *Portal) FillInitialHistory(user *User) error {
	if portal.bridge.Config.Bridge.InitialHistoryFill == 0 {
		return nil
	}
	endBackfill := portal.beginBackfill()
	defer endBackfill()
	if portal.privateChatBackfillInvitePuppet != nil {
		portal.privateChatBackfillInvitePuppet()
	}

	n := portal.bridge.Config.Bridge.InitialHistoryFill
	portal.log.Infoln("Filling initial history, maximum", n, "messages")
	resp, err := user.Conn.GetMessages(portal.Key.JID, "", strconv.Itoa(n))
	if err != nil {
		return err
	}
	portal.disableNotifications(user)
	portal.handleHistory(user, resp.Messages)
	portal.enableNotifications(user)
	portal.log.Infoln("Initial history fill complete")
	return nil
}

func (portal *Portal) handleHistory(user *User, messages []skype.Resource) {
	portal.log.Infoln("Handling", len(messages), "messages of history")
	for i, message := range messages {
		message = messages[len(messages)-i-1]
		if message.Content == "" {
			portal.log.Warnln("Message", message, "has no content")
			continue
		}
		if portal.privateChatBackfillInvitePuppet != nil && message.GetFromMe(user.Conn.Conn) && portal.IsPrivateChat() {
			portal.privateChatBackfillInvitePuppet()
		}
		t, _ := time.Parse(time.RFC3339, message.ComposeTime)
		message.Timestamp = t.Unix()
		portal.handleMessage(PortalMessage{ portal.Key.JID, user, message, uint64(message.Timestamp)})
	}
}

type BridgeInfoSection struct {
	ID          string              `json:"id"`
	DisplayName string              `json:"display_name,omitempty"`
	AvatarURL   id.ContentURIString `json:"avatar_url,omitempty"`
	ExternalURL string              `json:"external_url,omitempty"`
}

type BridgeInfoContent struct {
	BridgeBot id.UserID          `json:"bridgebot"`
	Creator   id.UserID          `json:"creator,omitempty"`
	Protocol  BridgeInfoSection  `json:"protocol"`
	Network   *BridgeInfoSection `json:"network,omitempty"`
	Channel   BridgeInfoSection  `json:"channel"`
}

var (
	StateBridgeInfo         = event.Type{Type: "m.bridge", Class: event.StateEventType}
	StateHalfShotBridgeInfo = event.Type{Type: "uk.half-shot.bridge", Class: event.StateEventType}
)

func (portal *Portal) getBridgeInfo() (string, BridgeInfoContent) {
	bridgeInfo := BridgeInfoContent{
		BridgeBot: portal.bridge.Bot.UserID,
		Creator:   portal.MainIntent().UserID,
		Protocol: BridgeInfoSection{
			ID:          "skype",
			DisplayName: "Skype",
			AvatarURL:   id.ContentURIString(portal.bridge.Config.AppService.Bot.Avatar),
			ExternalURL: "https://www.skype.com/",
		},
		Channel: BridgeInfoSection{
			ID:          portal.Key.JID,
			DisplayName: portal.Name,
			AvatarURL:   portal.AvatarURL.CUString(),
		},
	}
	// bridgeInfoStateKey := fmt.Sprintf("net.maunium.whatsapp://whatsapp/%s", portal.Key.JID) ??
	bridgeInfoStateKey := portal.Key.JID
	return bridgeInfoStateKey, bridgeInfo
}

func (portal *Portal) UpdateBridgeInfo() {
	if len(portal.MXID) == 0 {
		portal.log.Debugln("Not updating bridge info: no Matrix room created")
		return
	}
	portal.log.Debugln("Updating bridge info...")
	stateKey, content := portal.getBridgeInfo()
	_, err := portal.MainIntent().SendStateEvent(portal.MXID, StateBridgeInfo, stateKey, content)
	if err != nil {
		portal.log.Warnln("Failed to update m.bridge:", err)
	}
	_, err = portal.MainIntent().SendStateEvent(portal.MXID, StateHalfShotBridgeInfo, stateKey, content)
	if err != nil {
		portal.log.Warnln("Failed to update uk.half-shot.bridge:", err)
	}
}

func (portal *Portal) CreateMatrixRoom(user *User) error {
	portal.roomCreateLock.Lock()
	defer portal.roomCreateLock.Unlock()
	if len(portal.MXID) > 0 {
		return nil
	}

	intent := portal.MainIntent()
	if err := intent.EnsureRegistered(); err != nil {
		return err
	}

	portal.log.Infoln("Creating Matrix room. Info source user.MXID:", user.MXID)
	portal.log.Infoln("Creating Matrix room. Info source portal.Key.JID:", portal.Key.JID)

	var metadata *skypeExt.GroupInfo
	if portal.IsPrivateChat() {
		puppet := portal.bridge.GetPuppetByJID(portal.Key.JID+skypeExt.NewUserSuffix)
		if portal.bridge.Config.Bridge.PrivateChatPortalMeta {
			portal.Name = puppet.Displayname
			portal.AvatarURL = puppet.AvatarURL
			portal.Avatar = puppet.Avatar
		} else {
			portal.Name = ""
		}
		portal.Topic = "skype private chat"
	} else if portal.IsStatusBroadcastRoom() {
		portal.Name = "skype Status Broadcast"
		portal.Topic = "skype status updates from your contacts"
	} else {
		var err error
		metadata, err = user.Conn.GetGroupMetaData(portal.Key.JID)
		if err == nil {
			portalName := ""
			noRoomTopic := false
			names := strings.Split(metadata.Name, ", ")
			for _, name := range names {
				key := "8:" + name + skypeExt.NewUserSuffix
				if key == user.JID {
					noRoomTopic = true
				}
			}
			if noRoomTopic {
				for index, participant := range metadata.Participants {
					fmt.Println()
					fmt.Printf("metadata.Participants2: %+v", participant)
					fmt.Println()

					if participant.JID == user.JID {
						continue
					}
					if contact, ok := user.Conn.Store.Contacts[participant.JID]; ok {
						if len(portalName) == 0 {
							portalName = contact.DisplayName
						} else {
							if index > 5 {
								portalName = portalName + ", ..."
								break
							} else {
								portalName = portalName + ", " + contact.DisplayName
							}
						}
					}
				}
				portal.Name = portalName
			} else {
				portal.Name = metadata.Name
			}
			// portal.Topic = metadata.Topic
			portal.Topic = ""
		}
		portal.UpdateAvatar(user, nil)
	}

	bridgeInfo := event.Content{
		Parsed: BridgeInfoContent{
			BridgeBot: portal.bridge.Bot.UserID,
			Creator:   portal.MainIntent().UserID,
			Protocol: BridgeInfoSection{
				ID:          "skype",
				DisplayName: "Skype",
				AvatarURL:   id.ContentURIString(portal.bridge.Config.AppService.Bot.Avatar),
				ExternalURL: "https://www.skype.com/",
			},
			Channel: BridgeInfoSection{
				ID: portal.Key.JID,
			},
		},
	}
	content := portal.GetBasePowerLevels()
	//if portal.IsPrivateChat() {
	//	// 创建单人会话时，使对方权限等级降低
	//	for userID, _ := range content.Users {
	//		content.Users[userID] = 100
	//	}
	// When creating a room, make user self the highest level of authority
	// content.Users[user.MXID] = 100
	//}
	// When creating a room, make user self the highest level of authority
	content.Users[user.MXID] = 95
	initialState := []*event.Event{{
		Type: event.StatePowerLevels,
		Content: event.Content{
			Parsed: content,
		},
	}, {
		Type:    StateBridgeInfo,
		Content: bridgeInfo,
	}, {
		// TODO remove this once https://github.com/matrix-org/matrix-doc/pull/2346 is in spec
		Type:    StateHalfShotBridgeInfo,
		Content: bridgeInfo,
	}}
	if !portal.AvatarURL.IsEmpty() {
		initialState = append(initialState, &event.Event{
			Type: event.StateRoomAvatar,
			Content: event.Content{
				Parsed: event.RoomAvatarEventContent{URL: portal.AvatarURL},
			},
		})
	}

	invite := []id.UserID{user.MXID}
	if user.IsRelaybot {
		invite = portal.bridge.Config.Bridge.Relaybot.InviteUsers
	}

	if portal.bridge.Config.Bridge.Encryption.Default {
		initialState = append(initialState, &event.Event{
			Type: event.StateEncryption,
			Content: event.Content{
				Parsed: event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1},
			},
		})
		portal.Encrypted = true
		if portal.IsPrivateChat() {
			invite = append(invite, portal.bridge.Bot.UserID)
		}
	}

	resp, err := intent.CreateRoom(&mautrix.ReqCreateRoom{
		Visibility:   "private",
		Name:         portal.Name,
		Topic:        portal.Topic,
		Invite:       invite,
		Preset:       "private_chat",
		IsDirect:     portal.IsPrivateChat(),
		InitialState: initialState,
	})
	if err != nil {
		return err
	}
	portal.MXID = resp.RoomID
	portal.Update()
	portal.bridge.portalsLock.Lock()
	portal.bridge.portalsByMXID[portal.MXID] = portal
	portal.bridge.portalsLock.Unlock()

	// We set the memberships beforehand to make sure the encryption key exchange in initial backfill knows the users are here.
	for _, user := range invite {
		portal.bridge.StateStore.SetMembership(portal.MXID, user, event.MembershipInvite)
	}

	if metadata != nil {
		portal.SyncParticipants(metadata)
	} else {
		fmt.Println("GetPuppetByCustomMXID: ", user.MXID)
		customPuppet := portal.bridge.GetPuppetByCustomMXID(user.MXID)
		if customPuppet != nil && customPuppet.CustomIntent() != nil {
			_ = customPuppet.CustomIntent().EnsureJoined(portal.MXID)
		}
	}
	inCommunity := user.addPortalToCommunity(portal)
	if portal.IsPrivateChat() {
		puppet := user.bridge.GetPuppetByJID(portal.Key.JID)
		user.addPuppetToCommunity(puppet)

		if portal.bridge.Config.Bridge.Encryption.Default {
			err = portal.bridge.Bot.EnsureJoined(portal.MXID)
			if err != nil {
				portal.log.Errorln("Failed to join created portal with bridge bot for e2be:", err)
			}
		}
	}
	user.CreateUserPortal(database.PortalKeyWithMeta{PortalKey: portal.Key, InCommunity: inCommunity})
	err = portal.FillInitialHistory(user)
	if err != nil {
		portal.log.Errorln("Failed to fill history:", err)
	}
	return nil
}

func (portal *Portal) IsPrivateChat() bool {
	if portal.isPrivate == nil {
		val := !strings.HasSuffix(portal.Key.JID, skypeExt.GroupSuffix)
		portal.isPrivate = &val
	}
	return *portal.isPrivate
}

func (portal *Portal) HasRelaybot() bool {
	if portal.bridge.Relaybot == nil {
		return false
	} else if portal.hasRelaybot == nil {
		val := portal.bridge.Relaybot.IsInPortal(portal.Key)
		portal.hasRelaybot = &val
	}
	return *portal.hasRelaybot
}

func (portal *Portal) IsStatusBroadcastRoom() bool {
	return portal.Key.JID == "status@broadcast"
}

func (portal *Portal) MainIntent() *appservice.IntentAPI {
	if portal.IsPrivateChat() {
		fmt.Println("IsPrivateChat")
		return portal.bridge.GetPuppetByJID(portal.Key.JID+skypeExt.NewUserSuffix).DefaultIntent()
	}
	fmt.Println("not IsPrivateChat")
	return portal.bridge.Bot
}

//func (portal *Portal) SetReply(content *event.MessageEventContent, info whatsapp.ContextInfo) {
//	if len(info.QuotedMessageID) == 0 {
//		return
//	}
//	message := portal.bridge.DB.Message.GetByJID(portal.Key, info.QuotedMessageID)
//	if message != nil {
//		evt, err := portal.MainIntent().GetEvent(portal.MXID, message.MXID)
//		if err != nil {
//			portal.log.Warnln("Failed to get reply target:", err)
//			return
//		}
//		content.SetReply(evt)
//	}
//	return
//}

func (portal *Portal) SetReplySkype(content *event.MessageEventContent, info skype.Resource) {
	if len(info.Id) == 0 {
		return
	}
	message := portal.bridge.DB.Message.GetByJID(portal.Key, info.Id)
	if message != nil {
		evt, err := portal.MainIntent().GetEvent(portal.MXID, message.MXID)
		if err != nil {
			portal.log.Warnln("Failed to get reply target:", err)
			return
		}
		if evt.Type == event.EventEncrypted {
			_ = evt.Content.ParseRaw(evt.Type)
			decryptedEvt, err := portal.bridge.Crypto.Decrypt(evt)
			if err != nil {
				portal.log.Warnln("Failed to decrypt reply target:", err)
			} else {
				evt = decryptedEvt
			}
		}
		_ = evt.Content.ParseRaw(evt.Type)
		content.SetReply(evt)
	}
	return
}

func (portal *Portal) HandleMessageRevokeSkype(user *User, message skype.Resource) {
	msg := portal.bridge.DB.Message.GetByJID(portal.Key, message.SkypeEditedId)
	if msg == nil {
		return
	}
	var intent *appservice.IntentAPI
	if message.GetFromMe(user.Conn.Conn) {
		if portal.IsPrivateChat() {
			intent = portal.bridge.GetPuppetByJID(user.JID).CustomIntent()
		}
		if intent == nil {
			intent = portal.bridge.GetPuppetByJID(user.JID).IntentFor(portal)
		}
	}
	if intent == nil {
		intent = portal.MainIntent()
	}
	_, err := intent.RedactEvent(portal.MXID, msg.MXID)
	if err != nil {
		// TODO Maybe there is a better implementation
		if strings.Index(err.Error(), "M_FORBIDDEN") > -1 {
			_, err = portal.MainIntent().RedactEvent(portal.MXID, msg.MXID)
		}
		portal.log.Errorln("Failed to redact %s: %v", msg.JID, err)
		return
	}
	msg.Delete()
}

//func (portal *Portal) HandleMessageRevoke(user *User, message whatsappExt.MessageRevocation) {
//	msg := portal.bridge.DB.Message.GetByJID(portal.Key, message.Id)
//	if msg == nil {
//		return
//	}
//	var intent *appservice.IntentAPI
//	if message.FromMe {
//		if portal.IsPrivateChat() {
//			intent = portal.bridge.GetPuppetByJID(user.JID).CustomIntent()
//		} else {
//			intent = portal.bridge.GetPuppetByJID(user.JID).IntentFor(portal)
//		}
//	} else if len(message.Participant) > 0 {
//		intent = portal.bridge.GetPuppetByJID(message.Participant).IntentFor(portal)
//	}
//	if intent == nil {
//		intent = portal.MainIntent()
//	}
//	_, err := intent.RedactEvent(portal.MXID, msg.MXID)
//	if err != nil {
//		portal.log.Errorln("Failed to redact %s: %v", msg.JID, err)
//		return
//	}
//	msg.Delete()
//}

func (portal *Portal) HandleFakeMessage(source *User, message FakeMessage) {
	if portal.isRecentlyHandled(message.ID) {
		return
	}

	content := event.MessageEventContent{
		MsgType: event.MsgNotice,
		Body:    message.Text,
	}
	if message.Alert {
		content.MsgType = event.MsgText
	}
	_, err := portal.sendMainIntentMessage(content)
	if err != nil {
		portal.log.Errorfln("Failed to handle fake message %s: %v", message.ID, err)
		return
	}

	portal.recentlyHandledLock.Lock()
	index := portal.recentlyHandledIndex
	portal.recentlyHandledIndex = (portal.recentlyHandledIndex + 1) % recentlyHandledLength
	portal.recentlyHandledLock.Unlock()
	portal.recentlyHandled[index] = message.ID
}

func (portal *Portal) sendMainIntentMessage(content interface{}) (*mautrix.RespSendEvent, error) {
	return portal.sendMessage(portal.MainIntent(), event.EventMessage, content, 0)
}

func (portal *Portal) sendMessage(intent *appservice.IntentAPI, eventType event.Type, content interface{}, timestamp int64) (*mautrix.RespSendEvent, error) {
	wrappedContent := event.Content{Parsed: content}
	if timestamp != 0 && intent.IsCustomPuppet {
		wrappedContent.Raw = map[string]interface{}{
			"net.maunium.skype.puppet": intent.IsCustomPuppet,
		}
	}
	fmt.Println("portal sendMessage timestamp:", timestamp)
	fmt.Printf("portal sendMessage: %+v", content)
	if portal.Encrypted && portal.bridge.Crypto != nil {
		encrypted, err := portal.bridge.Crypto.Encrypt(portal.MXID, eventType, wrappedContent)
		if err != nil {
			return nil, errors.Wrap(err, "failed to encrypt event")
		}
		eventType = event.EventEncrypted
		wrappedContent.Parsed = encrypted
	}
	if timestamp == 0 {
		return intent.SendMessageEvent(portal.MXID, eventType, &wrappedContent)
	} else {
		return intent.SendMassagedMessageEvent(portal.MXID, eventType, &wrappedContent, timestamp)
	}
}

func (portal *Portal) HandleTextMessage(source *User, message skype.Resource) {
	if message.ClientMessageId == "" && message.Content == "" && len(message.SkypeEditedId) > 0 {
		portal.HandleMessageRevokeSkype(source, message)
	} else {
		intent, endHandlePrivateChatFromMe := portal.startHandlingSkype(source, message)
		if endHandlePrivateChatFromMe != nil {
			defer endHandlePrivateChatFromMe()
		}
		if intent == nil {
			fmt.Println("portal HandleTextMessage0: ", intent)
			return
		}
		content := &event.MessageEventContent{
			Body:    message.Content,
			MsgType: event.MsgText,
		}

		portal.bridge.Formatter.ParseSkype(content, portal.MXID)

		// reedit message
		if len(message.SkypeEditedId) > 0 {
			message.ClientMessageId = message.SkypeEditedId + message.Id
			msg := source.bridge.DB.Message.GetByJID(portal.Key, message.SkypeEditedId)
			if msg != nil && len(msg.MXID) > 0 {
				inRelateTo := &event.RelatesTo{
					Type: event.RelReplace,
					EventID: msg.MXID,
				}
				content.SetRelatesTo(inRelateTo)
				content.NewContent = &event.MessageEventContent{
					MsgType: content.MsgType,
					Body: content.Body,
					FormattedBody: content.FormattedBody,
					Format: content.Format,
				}
			}
		}
		fmt.Printf("\nportal HandleTextMessage2: %+v", content)
		_, _ = intent.UserTyping(portal.MXID, false, 0)
		resp, err := portal.trySendMessage(intent, event.EventMessage, content, source, message)
		if err == nil {
			portal.finishHandlingSkype(source, &message, resp.EventID)
		}
	}
}

func (portal *Portal) trySendMessage(intent *appservice.IntentAPI, eventType event.Type, content interface{}, source *User, message skype.Resource) (resp *mautrix.RespSendEvent, err error) {
	resp, err = portal.sendMessage(intent, eventType, content, message.Timestamp * 1000)
	if err != nil {
		portal.log.Errorfln("Failed to handle message %s: %v", message.Id, err)
		if strings.Index(err.Error(), "M_UNKNOWN_TOKEN (HTTP 401)") > -1 {
			puppet := source.bridge.GetPuppetByJID(source.JID)
			err, accessToken := source.UpdateAccessToken(puppet)
			if err == nil && accessToken != "" {
				intent.AccessToken = accessToken
				resp, err = portal.sendMessage(intent, eventType, content, message.Timestamp * 1000)
				if err != nil {
					portal.log.Errorfln("Failed to handle message %s: %v", message.Id, err)
				}
			}
		}
	}
	return
}

func (portal *Portal) HandleLocationMessageSkype(source *User, message skype.Resource) {
	intent, endHandlePrivateChatFromMe := portal.startHandlingSkype(source, message)
	if endHandlePrivateChatFromMe != nil {
		defer endHandlePrivateChatFromMe()
	}
	if intent == nil {
		return
	}
	locationMessage, err:= message.ParseLocation()
	if err != nil {
		portal.log.Errorfln("Failed to parse contact message of %s: %v", message, err)
		return
	}

	latitude, _ := strconv.Atoi(locationMessage.Latitude)
	longitude, _:= strconv.Atoi(locationMessage.Longitude)
	geo := fmt.Sprintf("geo:%.6f,%.6f", float32(latitude)/1000000, float32(longitude)/1000000)
	content := &event.MessageEventContent{
		MsgType:       event.MsgText,
		Body:          fmt.Sprintf("Location: <a href='%s'>%s</a>%s<br>\n", locationMessage.A.Href, locationMessage.Address, geo),
		Format:        event.FormatHTML,
		FormattedBody: fmt.Sprintf("Location: <a href='%s'>%s</a>%s<br>\n", locationMessage.A.Href, locationMessage.Address, geo),
		GeoURI:        geo,
	}

	// portal.SetReplySkype(content, message)

	_, _ = intent.UserTyping(portal.MXID, false, 0)

	resp, err := portal.trySendMessage(intent, event.EventMessage, content, source, message)
	if err == nil {
		portal.finishHandlingSkype(source, &message, resp.EventID)
	}
	//resp, err := portal.sendMessage(intent, event.EventMessage, content, message.Timestamp * 1000)
	//if err != nil {
	//	portal.log.Errorfln("Failed to handle message %s: %v", message.Id, err)
	//	return
	//}
	//portal.finishHandlingSkype(source, &message, resp.EventID)
}

func (portal *Portal) HandleContactMessageSkype(source *User, message skype.Resource) {
	intent, endHandlePrivateChatFromMe := portal.startHandlingSkype(source, message)
	if endHandlePrivateChatFromMe != nil {
		defer endHandlePrivateChatFromMe()
	}
	if intent == nil {
		return
	}
	contactMessage, err:= message.ParseContact()
	if err != nil {
		portal.log.Errorfln("Failed to parse contact message of %s: %v", message, err)
		return
	}

	content := &event.MessageEventContent{
		Body:    fmt.Sprintf("%s\n%s", contactMessage.C.F, contactMessage.C.S),
		MsgType: event.MsgText,
	}

	// portal.SetReplySkype(content, message)

	_, _ = intent.UserTyping(portal.MXID, false, 0)
	resp, err := portal.trySendMessage(intent, event.EventMessage, content, source, message)
	if err == nil {
		portal.finishHandlingSkype(source, &message, resp.EventID)
	}
	//resp, err := portal.sendMessage(intent, event.EventMessage, content, message.Timestamp * 1000)
	//if err != nil {
	//	portal.log.Errorfln("Failed to handle message %s: %v", message.Id, err)
	//	return
	//}
	//portal.finishHandlingSkype(source, &message, resp.EventID)
}

func (portal *Portal) sendMediaBridgeFailureSkype(source *User, intent *appservice.IntentAPI, info skype.Resource, downloadErr error) {
	portal.log.Errorfln("Failed to download media for %s: %v", info.Id, downloadErr)
	resp, err := portal.trySendMessage(intent, event.EventMessage, &event.MessageEventContent{
		MsgType: event.MsgNotice,
		Body:    "Failed to bridge media",
	}, source, info)
	if err == nil {
		portal.finishHandlingSkype(source, &info, resp.EventID)
	} else {
		portal.log.Errorfln("Failed to send media download error message for %s: %v", info.Id, err)
	}
}

func (portal *Portal) encryptFile(data []byte, mimeType string) ([]byte, string, *event.EncryptedFileInfo) {
	if !portal.Encrypted {
		return data, mimeType, nil
	}

	file := &event.EncryptedFileInfo{
		EncryptedFile: *attachment.NewEncryptedFile(),
		URL:           "",
	}
	return file.Encrypt(data), "application/octet-stream", file

}

func (portal *Portal) tryKickUser(userID id.UserID, intent *appservice.IntentAPI) error {
	_, err := intent.KickUser(portal.MXID, &mautrix.ReqKickUser{UserID: userID})
	if err != nil {
		httpErr, ok := err.(mautrix.HTTPError)
		if ok && httpErr.RespError != nil && httpErr.RespError.ErrCode == "M_FORBIDDEN" {
			_, err = portal.MainIntent().KickUser(portal.MXID, &mautrix.ReqKickUser{UserID: userID})
		}
	}
	return err
}

func (portal *Portal) HandleMediaMessageSkype(source *User, download func(conn *skype.Conn, mediaType string) (data []byte, mediaMessage *skype.MediaMessageContent, err error), mediaType string, thumbnail []byte, info skype.Resource, sendAsSticker bool) {
	if info.ClientMessageId == "" && info.Content == "" && len(info.SkypeEditedId) > 0 {
		portal.HandleMessageRevokeSkype(source, info)
		return
	}

	intent, endHandlePrivateChatFromMe := portal.startHandlingSkype(source, info)
	if endHandlePrivateChatFromMe != nil {
		defer endHandlePrivateChatFromMe()
	}
	if intent == nil {
		return
	}

	data, mediaMessage, err := download(source.Conn.Conn, mediaType)
	if err == skype.ErrMediaDownloadFailedWith404 || err == skype.ErrMediaDownloadFailedWith410 {
		portal.log.Warnfln("Failed to download media for %s: %v. Calling LoadMediaInfo and retrying download...", info.Id, err)
		//_, err = source.Conn.LoadMediaInfo(info.RemoteJid, info.Id, info.FromMe)
		//if err != nil {
		//	portal.sendMediaBridgeFailure(source, intent, info, errors.Wrap(err, "failed to load media info"))
		//	return
		//}
		data, mediaMessage, err = download(source.Conn.Conn, mediaType)
	}
	if err == skype.ErrNoURLPresent {
		portal.log.Debugfln("No URL present error for media message %s, ignoring...", info.Id)
		return
	} else if err != nil {
		portal.sendMediaBridgeFailureSkype(source, intent, info, err)
		return
	}

	// synapse doesn't handle webp well, so we convert it. This can be dropped once https://github.com/matrix-org/synapse/issues/4382 is fixed
	mimeType := mimetype.Detect(data).String()
	if mimeType == "image/webp" {
		img, err := decodeWebp(bytes.NewReader(data))
		if err != nil {
			portal.log.Errorfln("Failed to decode media for %s: %v", err)
			return
		}

		var buf bytes.Buffer
		err = png.Encode(&buf, img)
		if err != nil {
			portal.log.Errorfln("Failed to convert media for %s: %v", err)
			return
		}
		data = buf.Bytes()
		mimeType = "image/png"
	}

	var width, height int
	if strings.HasPrefix(mimeType, "image/") {
		cfg, _, _ := image.DecodeConfig(bytes.NewReader(data))
		width, height = cfg.Width, cfg.Height
	}

	data, uploadMimeType, file := portal.encryptFile(data, mimeType)

	uploaded, err := intent.UploadBytes(data, uploadMimeType)
	if err != nil {
		portal.log.Errorfln("Failed to upload media for %s: %v", err)
		return
	}

	fileName := mediaMessage.OriginalName.V
	//exts, _ := mime.ExtensionsByType(mimeType)
	//if exts != nil && len(exts) > 0 {
	//	fileName += exts[0]
	//}
	duration, err := strconv.Atoi(mediaMessage.DurationMs)
	if err != nil {
		duration = 0
	}
	if mediaType == "RichText/Media_AudioMsg" {
		mimeType = "audio"
	}
	content := &event.MessageEventContent{
		Body: fileName,
		File: file,
		Info: &event.FileInfo{
			Size:     len(data),
			MimeType: mimeType,
			Width:    width,
			Height:   height,
			Duration: duration,
		},
	}
	if content.File != nil {
		content.File.URL = uploaded.ContentURI.CUString()
	} else {
		content.URL = uploaded.ContentURI.CUString()
	}
	// portal.SetReplySkype(content, info)

	fmt.Println()
	fmt.Println("mediaMessage.UrlThumbnail", mediaMessage.UrlThumbnail)
	fmt.Println()
	fmt.Printf("%+v", mediaMessage)
	fmt.Println()
	thumbnail, err = skype.Download(mediaMessage.UrlThumbnail, source.Conn.Conn, 0)
	if  err != nil {
		portal.log.Errorfln("Failed to download thumbnail for %s: %v", err)
	}

	if thumbnail != nil && portal.bridge.Config.Bridge.WhatsappThumbnail && err == nil {
		thumbnailMime := http.DetectContentType(thumbnail)
		thumbnailCfg, _, _ := image.DecodeConfig(bytes.NewReader(thumbnail))
		thumbnailSize := len(thumbnail)
		thumbnail, thumbnailUploadMime, thumbnailFile := portal.encryptFile(thumbnail, thumbnailMime)
		uploadedThumbnail, err := intent.UploadBytes(thumbnail, thumbnailUploadMime)
		if err != nil {
			portal.log.Warnfln("Failed to upload thumbnail for %s: %v", info.Id, err)
		} else if uploadedThumbnail != nil {
			if thumbnailFile != nil {
				thumbnailFile.URL = uploadedThumbnail.ContentURI.CUString()
				content.Info.ThumbnailFile = thumbnailFile
			} else {
				content.Info.ThumbnailURL = uploadedThumbnail.ContentURI.CUString()
			}
			content.Info.ThumbnailInfo = &event.FileInfo{
				Size:     thumbnailSize,
				Width:    thumbnailCfg.Width,
				Height:   thumbnailCfg.Height,
				MimeType: thumbnailMime,
			}
			fmt.Println("content.Info")
			fmt.Printf("%+v", content)
			fmt.Println()
			fmt.Printf("%+v", *content.Info.ThumbnailInfo)
			fmt.Println()
			fmt.Println()
			fmt.Printf("%+v", content.Info.ThumbnailInfo)
			fmt.Println()
		}
	}

	switch strings.ToLower(strings.Split(mimeType, "/")[0]) {
	case "image":
		if !sendAsSticker {
			content.MsgType = event.MsgImage
		}
	case "video":
		content.MsgType = event.MsgVideo
	case "audio":
		content.MsgType = event.MsgAudio
	default:
		content.MsgType = event.MsgFile
	}

	_, _ = intent.UserTyping(portal.MXID, false, 0)
	eventType := event.EventMessage
	if sendAsSticker {
		eventType = event.EventSticker
	}

	resp, err := portal.trySendMessage(intent, eventType, content, source, info)
	if err == nil {
		portal.finishHandlingSkype(source, &info, resp.EventID)
	}

	//if len(caption) > 0 {
	//	captionContent := &event.MessageEventContent{
	//		Body:    caption,
	//		MsgType: event.MsgNotice,
	//	}
	//
	//	portal.bridge.Formatter.ParseSkype(captionContent)
	//
	//	_, err := portal.sendMessage(intent, event.EventMessage, captionContent, ts)
	//	if err != nil {
	//		portal.log.Warnfln("Failed to handle caption of message %s: %v", info.Id, err)
	//	}
	//	// TODO store caption mxid?
	//}
}

func makeMessageID() *string {
	b := make([]byte, 10)
	rand.Read(b)
	str := strings.ToUpper(hex.EncodeToString(b))
	return &str
}

func (portal *Portal) downloadThumbnail(content *event.MessageEventContent, id id.EventID) []byte {
	if len(content.GetInfo().ThumbnailURL) == 0 {
		return nil
	}
	mxc, err := content.GetInfo().ThumbnailURL.Parse()
	if err != nil {
		portal.log.Errorln("Malformed thumbnail URL in %s: %v", id, err)
	}
	thumbnail, err := portal.MainIntent().DownloadBytes(mxc)
	if err != nil {
		portal.log.Errorln("Failed to download thumbnail in %s: %v", id, err)
		return nil
	}
	thumbnailType := http.DetectContentType(thumbnail)
	var img image.Image
	switch thumbnailType {
	case "image/png":
		img, err = png.Decode(bytes.NewReader(thumbnail))
	case "image/gif":
		img, err = gif.Decode(bytes.NewReader(thumbnail))
	case "image/jpeg":
		return thumbnail
	default:
		return nil
	}
	var buf bytes.Buffer
	err = jpeg.Encode(&buf, img, &jpeg.Options{
		Quality: jpeg.DefaultQuality,
	})
	if err != nil {
		portal.log.Errorln("Failed to re-encode thumbnail in %s: %v", id, err)
		return nil
	}
	return buf.Bytes()
}

func (portal *Portal) preprocessMatrixMediaSkype(relaybotFormatted bool, content *event.MessageEventContent, eventID id.EventID) (string, uint64, []byte) {
	var caption string
	if relaybotFormatted {
		caption = portal.bridge.Formatter.ParseMatrix(content.FormattedBody)
	}

	var file *event.EncryptedFileInfo
	rawMXC := content.URL
	if content.File != nil {
		file = content.File
		rawMXC = file.URL
	}
	mxc, err := rawMXC.Parse()
	if err != nil {
		portal.log.Errorln("Malformed content URL in %s: %v", eventID, err)
		return "", 0, nil
	}
	data, err := portal.MainIntent().DownloadBytes(mxc)
	if err != nil {
		portal.log.Errorfln("Failed to download media in %s: %v", eventID, err)
		return "", 0, nil
	}
	if file != nil {
		data, err = file.Decrypt(data)
		if err != nil {
			portal.log.Errorfln("Failed to decrypt media in %s: %v", eventID, err)
			return "", 0, nil
		}
	}

	return caption, uint64(len(data)), data
}

type MediaUpload struct {
	Caption       string
	URL           string
	MediaKey      []byte
	FileEncSHA256 []byte
	FileSHA256    []byte
	FileLength    uint64
	Thumbnail     []byte
}

func (portal *Portal) sendMatrixConnectionError(sender *User, eventID id.EventID) bool {
	if !sender.HasSession() {
		portal.log.Debugln("Ignoring event", eventID, "from", sender.MXID, "as user has no session")
		return true
	} else if !sender.IsConnected() {
		portal.log.Debugln("Ignoring event", eventID, "from", sender.MXID, "as user is not connected")
		inRoom := ""
		if portal.IsPrivateChat() {
			inRoom = " in your management room"
		}
		reconnect := fmt.Sprintf("Use `%s reconnect`%s to reconnect.", portal.bridge.Config.Bridge.CommandPrefix, inRoom)
		if sender.IsLoginInProgress() {
			reconnect = "You have a login attempt in progress, please wait."
		}
		msg := format.RenderMarkdown("\u26a0 You are not connected to skype, so your message was not bridged. "+reconnect, true, false)
		msg.MsgType = event.MsgNotice
		_, err := portal.sendMainIntentMessage(msg)
		if err != nil {
			portal.log.Errorln("Failed to send bridging failure message:", err)
		}
		return true
	}
	return false
}

func (portal *Portal) addRelaybotFormat(sender *User, content *event.MessageEventContent) bool {
	member := portal.MainIntent().Member(portal.MXID, sender.MXID)
	if len(member.Displayname) == 0 {
		member.Displayname = string(sender.MXID)
	}

	if content.Format != event.FormatHTML {
		content.FormattedBody = strings.Replace(html.EscapeString(content.Body), "\n", "<br/>", -1)
		content.Format = event.FormatHTML
	}
	data, err := portal.bridge.Config.Bridge.Relaybot.FormatMessage(content, sender.MXID, member)
	if err != nil {
		portal.log.Errorln("Failed to apply relaybot format:", err)
	}
	content.FormattedBody = data
	return true
}

func (portal *Portal) convertMatrixMessageSkype(sender *User, evt *event.Event) (*skype.SendMessage, *User, *event.MessageEventContent) {
	content, ok := evt.Content.Parsed.(*event.MessageEventContent)
	if !ok {
		portal.log.Debugfln("Failed to handle event %s: unexpected parsed content type %T", evt.ID, evt.Content.Parsed)
		return nil, sender, content
	}

	currentTimeNanoStr := strconv.FormatInt(time.Now().UnixNano(), 10)
	currentTimeNanoStr = currentTimeNanoStr[:len(currentTimeNanoStr)-3]
	clientMessageId := currentTimeNanoStr + fmt.Sprintf("%04v", rand.New(rand.NewSource(time.Now().UnixNano())).Intn(10000))
	info := &skype.SendMessage{
		ClientMessageId: clientMessageId,
		Jid: portal.Key.JID,//receiver id(conversation id)
		Timestamp: time.Now().Unix(),
	}

	replyToID := content.GetReplyTo()

	// reedit message
	if content.NewContent != nil {
		a := strings.Replace(sender.JID, skypeExt.NewUserSuffix, "", 1)
		a = strings.Replace(a, "8:", "", 1)
		tsMs := strconv.FormatInt(time.Now().UnixNano()/1e6, 10)
		r := []rune(tsMs)
		ts := string(r[:len(r) - 3])
		msg := portal.bridge.DB.Message.GetByMXID(content.RelatesTo.EventID)
		if msg != nil && len(msg.JID) > 0 {
			info.SkypeEditedId = msg.JID
			//info.ClientMessageId = info.ClientMessageId + info.SkypeEditedId
			content.Body = content.Body + fmt.Sprintf("<e_m a=\"%s\" ts_ms=\"%s\" ts=\"%s\" t=\"61\"></e_m>", a, tsMs, ts)
			content.Body = strings.TrimPrefix(content.Body, " * ")
			if len(content.FormattedBody) > 0 {
				content.FormattedBody = content.FormattedBody + fmt.Sprintf("<e_m a=\"%s\" ts_ms=\"%s\" ts=\"%s\" t=\"61\"></e_m>", a, tsMs, ts)
				content.FormattedBody = strings.TrimPrefix(content.FormattedBody, " * ")
			}
		}

		// in reedit message we can't obtain the "relayId" from RelatesTo.EventID cause the matrix message doesn't put it in "RelatesTo".
		// so i get relayId with use regexp, but it's not a good way,
		// now there is no way to get relayId if reedit message with add a mention user
		// TODO maybe we can record the relayId to DB
		rQuote := regexp.MustCompile(`<mx-reply><blockquote><a href=".*#/room/` + string(portal.MXID) + `/(.*)\?via=.*">In reply to.*</blockquote></mx-reply>(.*)`)
		quoteMatches := rQuote.FindAllStringSubmatch(content.FormattedBody, -1)
		if len(replyToID) < 1 {
			if len(quoteMatches) > 0 {
				if len(quoteMatches[0]) > 0 {
					replyToID = id.EventID(quoteMatches[0][1])
				}

				//Filter out the " * " in the matrix editing message (i don't why the matrix need a * in the edit message body)
				if len(quoteMatches[0]) > 1 {
					needReplace := quoteMatches[0][2]
					afterReplace := strings.TrimPrefix(needReplace, " * ")
					content.Body = strings.Replace(content.Body, needReplace, afterReplace, 1)
					content.FormattedBody = strings.Replace(content.FormattedBody, needReplace, afterReplace, 1)
				}
			}
		}
	}

	// reply message
	var newContent string
	backStr := ""
	if len(replyToID) > 0 {
		rQuote := regexp.MustCompile(`<mx-reply>(.*?)</mx-reply>(.*)`)
		quoteMatches := rQuote.FindAllStringSubmatch(content.FormattedBody, -1)
		if len(quoteMatches) > 0 {
			if len(quoteMatches[0]) > 2 {
				backStr = quoteMatches[0][2]
			}
		}

		content.RemoveReplyFallback()
		msg := portal.bridge.DB.Message.GetByMXID(replyToID)
		if msg != nil && len(msg.Content) > 0 {
			messageId := msg.ID
			if len(messageId) < 1 {
				messageId = strconv.FormatInt(time.Now().UnixNano()/1e6, 10)
			}
			author := strings.Replace(msg.Sender, skypeExt.NewUserSuffix, "", 1)
			author = strings.Replace(author, "8:", "", 1)
			conversation := msg.Chat.Receiver
			cuid := msg.JID
			r := []rune(messageId)
			timestamp := string(r[:len(r) - 3])
			quoteMessage := msg.Content

			puppet := sender.bridge.GetPuppetByJID(msg.Sender)

			newContent = fmt.Sprintf(`<quote author="%s" authorname="%s" timestamp="%s" conversation="%s" messageid="%s" cuid="%s"><legacyquote>[%s] %s: </legacyquote>%s<legacyquote>\n\n&lt;&lt;&lt; </legacyquote></quote>`,
				author,
				puppet.Displayname,
				timestamp,
				conversation,
				messageId,
				cuid,
				timestamp,
				puppet.Displayname,
				quoteMessage)
			content.FormattedBody = newContent
		}
	}

	relaybotFormatted := false
	if sender.NeedsRelaybot(portal) {
		if !portal.HasRelaybot() {
			if sender.HasSession() {
				portal.log.Debugln("Database says", sender.MXID, "not in chat and no relaybot, but trying to send anyway")
			} else {
				portal.log.Debugln("Ignoring message from", sender.MXID, "in chat with no relaybot")
				return nil, sender, content
			}
		} else {
			relaybotFormatted = portal.addRelaybotFormat(sender, content)
			sender = portal.bridge.Relaybot
		}
	}
	if evt.Type == event.EventSticker {
		content.MsgType = event.MsgImage
	}

	fmt.Println("convertMatrixMessage content.MsgType: ", content.MsgType)
	fmt.Println("convertMatrixMessage content.Body: ", content.Body)
	fmt.Println("convertMatrixMessage content.NewBody: ", content.NewContent)
	fmt.Println("convertMatrixMessage content.FormattedBody: ", content.FormattedBody)
	info.Type = string(content.MsgType)
	switch content.MsgType {
	case event.MsgText, event.MsgEmote, event.MsgNotice:
		text := content.Body
		if content.Format == event.FormatHTML {
			text = portal.bridge.Formatter.ParseMatrix(content.FormattedBody)
		}
		if content.MsgType == event.MsgEmote && !relaybotFormatted {
			text = "/me " + text
		}
		if len(content.FormattedBody) > 0 {
			matchStr := content.FormattedBody
			if len(backStr) > 0 {
				matchStr = backStr
			}

			// mention user message
			r := regexp.MustCompile(`(?m)<a[^>]+\bhref="(.*?)://` + portal.bridge.Config.Homeserver.ServerName + `/#/@([^"]+):(.*?)">(.*?)</a>`)
			matches := r.FindAllStringSubmatch(matchStr, -1)
			fmt.Println("matches: ", matches)
			if len(matches) > 0 {
				for _, match := range matches {
					if len(match) > 2 {
						skyId := patch.ParseLocalPart(html.UnescapeString(match[2]), false)
						skyId = strings.ReplaceAll(skyId, "skype&", "")
						skyId = strings.ReplaceAll(skyId, "-", ":")
						// Adapt to the message format sent by the matrix front end
						matchStr = strings.ReplaceAll(matchStr, match[0] + ":", fmt.Sprintf(`<at id="%s">%s</at>`, skyId, match[4]))
						matchStr = strings.ReplaceAll(matchStr, match[0], fmt.Sprintf(`<at id="%s">%s</at>`, skyId, match[4]))
					}
				}
				if len(backStr) > 0 {
					content.FormattedBody = content.FormattedBody + matchStr
				} else {
					content.FormattedBody = matchStr
				}
			} else {
				if len(backStr) > 0 {
					content.FormattedBody = content.FormattedBody + backStr
				}
			}
		}

		if len(content.FormattedBody) > 0 {
			info.SendTextMessage = &skype.SendTextMessage{
				Content : content.FormattedBody,
			}
		} else {
			info.SendTextMessage = &skype.SendTextMessage{
				Content : content.Body,
			}
		}
	case event.MsgImage:
		caption, fileSize , data := portal.preprocessMatrixMediaSkype(relaybotFormatted, content, evt.ID)
		//if media == nil {
		//	return nil, sender, content
		//}
		fmt.Println("caption: ", caption)
		info.SendMediaMessage = &skype.SendMediaMessage{
			FileName: content.Body,
			FileType: content.GetInfo().MimeType,
			RawData:  data,
			FileSize: strconv.FormatUint(fileSize, 10), // strconv.FormatUint(fileSize, 10),
			Duration: 0,
		}
	case event.MsgVideo:
		_, fileSize , data := portal.preprocessMatrixMediaSkype(relaybotFormatted, content, evt.ID)
		duration := uint32(content.GetInfo().Duration)
		info.SendMediaMessage = &skype.SendMediaMessage{
			FileName: content.Body,
			FileType: content.GetInfo().MimeType,
			RawData:  data,
			FileSize: strconv.FormatUint(fileSize, 10), // strconv.FormatUint(fileSize, 10),
			Duration: int(duration),
		}
	case event.MsgAudio:
		_, fileSize , data := portal.preprocessMatrixMediaSkype(relaybotFormatted, content, evt.ID)
		duration := uint32(content.GetInfo().Duration)
		info.SendMediaMessage = &skype.SendMediaMessage{
			FileName: content.Body,
			FileType: content.GetInfo().MimeType,
			RawData:  data,
			FileSize: strconv.FormatUint(fileSize, 10), // strconv.FormatUint(fileSize, 10),
			Duration: int(duration),
		}
	case event.MsgFile:
		_, fileSize , data := portal.preprocessMatrixMediaSkype(relaybotFormatted, content, evt.ID)
		info.SendMediaMessage = &skype.SendMediaMessage{
			FileName: content.Body,
			FileType: content.GetInfo().MimeType,
			RawData:  data,
			FileSize: strconv.FormatUint(fileSize, 10), // strconv.FormatUint(fileSize, 10),
			Duration: 0,
		}
	default:
		portal.log.Debugln("Unhandled Matrix event %s: unknown msgtype %s", evt.ID, content.MsgType)
		return nil, sender, content
	}
	return info, sender, content
}

func (portal *Portal) wasMessageSent(sender *User, id string) bool {
	//_, err := sender.Conn.LoadMessagesAfter(portal.Key.JID, id, true, 0)
	//if err != nil {
	//	if err != whatsapp.ErrServerRespondedWith404 {
	//		portal.log.Warnfln("Failed to check if message was bridged without response: %v", err)
	//	}
	//	return false
	//}
	return true
}

func (portal *Portal) sendErrorMessage(sendErr error) id.EventID {
	resp, err := portal.sendMainIntentMessage(event.MessageEventContent{
		MsgType: event.MsgNotice,
		Body:    fmt.Sprintf("\u26a0 Your message may not have been bridged: %v", sendErr),
	})
	if err != nil {
		portal.log.Warnfln("Failed to send bridging error message:", err)
		return ""
	}
	return resp.EventID
}

func (portal *Portal) sendDeliveryReceipt(eventID id.EventID) {
	if portal.bridge.Config.Bridge.DeliveryReceipts {
		err := portal.bridge.Bot.MarkRead(portal.MXID, eventID)
		if err != nil {
			portal.log.Debugfln("Failed to send delivery receipt for %s: %v", eventID, err)
		}
	}
}

var timeout = errors.New("message sending timed out")

func (portal *Portal) HandleMatrixMessage(sender *User, evt *event.Event) {
	fmt.Println("portal HandleMatrixMessage sender.JID: ", sender.JID)
	fmt.Println("portal HandleMatrixMessage portal.Key.Receiver: ", portal.Key.Receiver)
	fmt.Println("portal HandleMatrixMessage portal.Key.JID: ", portal.Key.JID)
	if !portal.HasRelaybot() && (
		(portal.IsPrivateChat() && sender.JID != portal.Key.Receiver) ||
			portal.sendMatrixConnectionError(sender, evt.ID)) {
		return
	}
	portal.log.Debugfln("Received event %s", evt.ID)
	info, sender, _ := portal.convertMatrixMessageSkype(sender, evt)
	if info == nil {
		fmt.Println("portal HandleMatrixMessage info is nil: ")
		return
	}

	var content string
	if info.Type != string(event.MsgText) {
		content = info.SendMediaMessage.FileName // URIObject
	} else {
		content = info.SendTextMessage.Content
	}

	fmt.Println("portal HandleMatrixMessage start markHandledSkype: ")
	portal.markHandledSkype(sender, &skype.Resource{
		ClientMessageId: info.ClientMessageId,
		Jid: portal.Key.JID,//receiver id(conversation id)
		Timestamp: time.Now().Unix(),
		Content: content,
	}, evt.ID)
	portal.log.Debugln("Sending event", evt.ID, "to Skype")

	errChan := make(chan error, 1)
	//go sender.Conn.Conn.SendMsg(portal.Key.JID, info.Content, info.ClientMessageId, errChan)
	go SendMsg(sender, portal.Key.JID, info, errChan)
	var err error
	var errorEventID id.EventID
	select {
	case err = <-errChan:
	case <-time.After(time.Duration(portal.bridge.Config.Bridge.ConnectionTimeout) * time.Second):
		if portal.bridge.Config.Bridge.FetchMessageOnTimeout && portal.wasMessageSent(sender, info.ClientMessageId) {
			portal.log.Debugln("Matrix event %s was bridged, but response didn't arrive within timeout")
			portal.sendDeliveryReceipt(evt.ID)
		} else {
			portal.log.Warnfln("Response when bridging Matrix event %s is taking long to arrive", evt.ID)
			errorEventID = portal.sendErrorMessage(timeout)
		}
		err = <-errChan
	}
	if err != nil {
		portal.log.Errorfln("Error handling Matrix event %s: %v", evt.ID, err)
		portal.sendErrorMessage(err)
	} else {
		portal.log.Debugfln("Handled Matrix event %s", evt.ID)
		portal.sendDeliveryReceipt(evt.ID)
	}
	if errorEventID != "" {
		_, err = portal.MainIntent().RedactEvent(portal.MXID, errorEventID)
		if err != nil {
			portal.log.Warnfln("Failed to redact timeout warning message %s: %v", errorEventID, err)
		}
	}
}

func SendMsg(sender *User, chatThreadId string, content *skype.SendMessage, output chan<- error) (err error) {
	fmt.Println("message SendMsg type: ", content.Type)
	if sender.Conn.LoginInfo != nil {
		switch event.MessageType(content.Type) {
		case event.MsgText, event.MsgEmote, event.MsgNotice:
			err = sender.Conn.SendText(chatThreadId, content)
		case event.MsgImage:
			fmt.Println("message SendMsg type m.image: ", content.Type)
			err = sender.Conn.SendFile(chatThreadId, content)
		case event.MsgVideo:
			fmt.Println("message SendMsg type m.video: ", content.Type)
			err = sender.Conn.SendFile(chatThreadId, content)
		case event.MsgAudio:
			fmt.Println("message SendMsg type m.audio: ", content.Type)
			err = sender.Conn.SendFile(chatThreadId, content)
		case event.MsgFile:
			fmt.Println("message SendMsg type m.file: ", content.Type)
			err = sender.Conn.SendFile(chatThreadId, content)
		case event.MsgLocation:
			fmt.Println("message SendMsg type m.location: ", content.Type)
			//err = c.SendFile(chatThreadId, content)
		default:
			err = errors.New("send to skype(unknown message type)")
		}
	} else {
		err = errors.New("Not logged into Skype or Skype session has expired")
	}

	if err != nil {
		output <- err
	} else {
		output <- nil
	}
	return
}

func (portal *Portal) HandleMatrixRedaction(sender *User, evt *event.Event) {
	if portal.IsPrivateChat() && sender.JID != portal.Key.Receiver {
		return
	}

	msg := portal.bridge.DB.Message.GetByMXID(evt.Redacts)
	if msg == nil || msg.Sender != sender.JID {
		return
	}

	errChan := make(chan error, 1)
	//todo  return errChan here
	go sender.Conn.DeleteMessage(msg.Chat.JID, msg.ID)

	var err error
	select {
	case err = <-errChan:
	case <-time.After(time.Duration(portal.bridge.Config.Bridge.ConnectionTimeout) * time.Second):
		portal.log.Warnfln("Response when bridging Matrix redaction %s is taking long to arrive", evt.ID)
		err = <-errChan
	}
	if err != nil {
		portal.log.Errorfln("Error handling Matrix redaction %s: %v", evt.ID, err)
	} else {
		portal.log.Debugln("Handled Matrix redaction %s of %s", evt.ID, evt.Redacts)
		portal.sendDeliveryReceipt(evt.ID)
	}
}

func (portal *Portal) Delete() {
	portal.Portal.Delete()
	portal.bridge.portalsLock.Lock()
	delete(portal.bridge.portalsByJID, portal.Key)
	if len(portal.MXID) > 0 {
		delete(portal.bridge.portalsByMXID, portal.MXID)
	}
	portal.bridge.portalsLock.Unlock()
}

func (portal *Portal) GetMatrixUsers() ([]id.UserID, error) {
	members, err := portal.MainIntent().JoinedMembers(portal.MXID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get member list")
	}
	var users []id.UserID
	for userID := range members.Joined {
		_, isPuppet := portal.bridge.ParsePuppetMXID(userID)
		if !isPuppet && userID != portal.bridge.Bot.UserID {
			users = append(users, userID)
		}
	}
	return users, nil
}

func (portal *Portal) GetPuppets() ([]struct {
	DisplayName *string `json:"display_name"`
	AvatarURL   *string `json:"avatar_url"`
}, error) {
	members, err := portal.MainIntent().JoinedMembers(portal.MXID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get member list")
	}
	var puppets []struct {
		DisplayName *string `json:"display_name"`
		AvatarURL   *string `json:"avatar_url"`
	}
	for userID := range members.Joined {
		_, isPuppet := portal.bridge.ParsePuppetMXID(userID)
		if isPuppet && userID != portal.bridge.Bot.UserID {
			puppets = append(puppets, members.Joined[userID])
		}
	}
	return puppets, nil
}

func (portal *Portal) CleanupIfEmpty() {
	users, err := portal.GetMatrixUsers()
	if err != nil {
		portal.log.Errorfln("Failed to get Matrix user list to determine if portal needs to be cleaned up: %v", err)
		return
	}

	if len(users) == 0 {
		portal.log.Infoln("Room seems to be empty, cleaning up...")
		portal.Delete()
		portal.Cleanup(false)
	}
}

func (portal *Portal) Cleanup(puppetsOnly bool) {
	if len(portal.MXID) == 0 {
		return
	}
	if portal.IsPrivateChat() {
		_, err := portal.MainIntent().LeaveRoom(portal.MXID)
		if err != nil {
			portal.log.Warnln("Failed to leave private chat portal with main intent:", err)
		}
		return
	}
	intent := portal.MainIntent()
	members, err := intent.JoinedMembers(portal.MXID)
	if err != nil {
		portal.log.Errorln("Failed to get portal members for cleanup:", err)
		return
	}
	for member, _ := range members.Joined {
		if member == intent.UserID {
			continue
		}
		puppet := portal.bridge.GetPuppetByMXID(member)
		if puppet != nil {
			_, err = puppet.DefaultIntent().LeaveRoom(portal.MXID)
			if err != nil {
				portal.log.Errorln("Error leaving as puppet while cleaning up portal:", err)
			}
		} else if !puppetsOnly {
			_, err = intent.KickUser(portal.MXID, &mautrix.ReqKickUser{UserID: member, Reason: "Deleting portal"})
			if err != nil {
				content := format.RenderMarkdown("Error leaving room(Deleting portal from skype), you can leave this room manually.", true, false)
				content.MsgType = event.MsgNotice
				_, _ = portal.MainIntent().SendMessageEvent(portal.MXID, event.EventMessage, content)
				portal.log.Errorln("Error kicking user while cleaning up portal:", err)
			}
		}
	}
	_, err = intent.LeaveRoom(portal.MXID)
	if err != nil {
		portal.log.Errorln("Error leaving with main intent while cleaning up portal:", err)
	}
}

func (portal *Portal) HandleMatrixLeave(sender *User) {
	if portal.IsPrivateChat() {
		portal.log.Debugln("User left private chat portal, cleaning up and deleting...")
		portal.Delete()
		portal.Cleanup(false)
		return
	} else {
		// TODO should we somehow deduplicate this call if this leave was sent by the bridge?
		err := sender.Conn.HandleGroupLeave(portal.Key.JID)
		if err != nil {
			portal.log.Errorfln("Failed to leave group as %s: %v", sender.MXID, err)
			return
		}
		//portal.log.Infoln("Leave response:", <-resp)
		portal.CleanupIfEmpty()
	}
}

func (portal *Portal) HandleMatrixKick(sender *User, evt *event.Event) {
	jid, _:= portal.bridge.ParsePuppetMXID(id.UserID(evt.GetStateKey()))
	puppet := portal.bridge.GetPuppetByJID(jid)
	if puppet != nil {
		jid = strings.Replace(jid, skypeExt.NewUserSuffix, "", 1)
		err := sender.Conn.HandleGroupKick(portal.Key.JID, []string{jid})
		if err != nil {
			portal.log.Errorfln("Failed to kick %s from group as %s: %v", puppet.JID, sender.MXID, err)
			return
		}
	}
}

func (portal *Portal) HandleMatrixInvite(sender *User, evt *event.Event) {
	jid, _:= portal.bridge.ParsePuppetMXID(id.UserID(evt.GetStateKey()))
	puppet := portal.bridge.GetPuppetByJID(jid)
	if puppet != nil {
		jid = strings.Replace(jid, skypeExt.NewUserSuffix, "", 1)
		err := sender.Conn.HandleGroupInvite(portal.Key.JID, []string{jid})
		if err != nil {
			portal.log.Errorfln("Failed to add %s to group as %s: %v", puppet.JID, sender.MXID, err)
			return
		}
	}
}
