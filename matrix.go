package main

import (
	"fmt"
	skype "github.com/kelaresg/go-skypeapi"
	"github.com/kelaresg/matrix-skype/database"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/patch"
	"strconv"
	"strings"
	"time"

	"maunium.net/go/maulogger/v2"

	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

type MatrixHandler struct {
	bridge *Bridge
	as     *appservice.AppService
	log    maulogger.Logger
	cmd    *CommandHandler
}

func NewMatrixHandler(bridge *Bridge) *MatrixHandler {
	handler := &MatrixHandler{
		bridge: bridge,
		as:     bridge.AS,
		log:    bridge.Log.Sub("Matrix"),
		cmd:    NewCommandHandler(bridge),
	}
	bridge.EventProcessor.On(event.EventMessage, handler.HandleMessage)
	bridge.EventProcessor.On(event.EventEncrypted, handler.HandleEncrypted)
	bridge.EventProcessor.On(event.EventSticker, handler.HandleMessage)
	bridge.EventProcessor.On(event.EventRedaction, handler.HandleRedaction)
	bridge.EventProcessor.On(event.StateMember, handler.HandleMembership)
	bridge.EventProcessor.On(event.StateRoomName, handler.HandleRoomMetadata)
	bridge.EventProcessor.On(event.StateRoomAvatar, handler.HandleRoomMetadata)
	bridge.EventProcessor.On(event.StateTopic, handler.HandleRoomMetadata)
	bridge.EventProcessor.On(event.StateEncryption, handler.HandleEncryption)
	return handler
}

func (mx *MatrixHandler) HandleEncryption(evt *event.Event) {
	if evt.Content.AsEncryption().Algorithm != id.AlgorithmMegolmV1 {
		return
	}
	portal := mx.bridge.GetPortalByMXID(evt.RoomID)
	if portal != nil && !portal.Encrypted {
		mx.log.Debugfln("%s enabled encryption in %s", evt.Sender, evt.RoomID)
		portal.Encrypted = true
		portal.Update()
	}
}

func (mx *MatrixHandler) joinAndCheckMembers(evt *event.Event, intent *appservice.IntentAPI) *mautrix.RespJoinedMembers {
	resp, err := intent.JoinRoomByID(evt.RoomID)
	if err != nil {
		mx.log.Debugfln("JoinRoomByID err, retry in 5 seconds", err)
		time.Sleep(5 * time.Second)
		resp, err = intent.JoinRoomByID(evt.RoomID)
		if err != nil {
			mx.log.Debugfln("JoinRoomByID err, retry again in 5 seconds", err)
			time.Sleep(5 * time.Second)
			resp, err = intent.JoinRoomByID(evt.RoomID)
			if err != nil {
				mx.log.Debugfln("Failed to join room %s as %s with invite from %s: %v", evt.RoomID, intent.UserID, evt.Sender, err)
				return nil
			}
		}
	}

	members, err := intent.JoinedMembers(resp.RoomID)
	if err != nil {
		mx.log.Debugfln("Failed to get members in room %s after accepting invite from %s as %s: %v", resp.RoomID, evt.Sender, intent.UserID, err)
		_, _ = intent.LeaveRoom(resp.RoomID)
		return nil
	}

	if len(members.Joined) < 2 {
		mx.log.Debugln("Leaving empty room", resp.RoomID, "after accepting invite from", evt.Sender, "as", intent.UserID)
		_, _ = intent.LeaveRoom(resp.RoomID)
		return nil
	}
	return members
}

func (mx *MatrixHandler) HandleBotInvite(evt *event.Event) {
	intent := mx.as.BotIntent()

	user := mx.bridge.GetUserByMXID(evt.Sender)
	if user == nil {
		return
	}

	members := mx.joinAndCheckMembers(evt, intent)
	if members == nil {
		return
	}

	if !user.Whitelisted {
		_, _ = intent.SendNotice(evt.RoomID, "You are not whitelisted to use this bridge.\n"+
			"If you're the owner of this bridge, see the bridge.permissions section in your config file.")
		_, _ = intent.LeaveRoom(evt.RoomID)
		return
	}

	if evt.RoomID == mx.bridge.Config.Bridge.Relaybot.ManagementRoom {
		_, _ = intent.SendNotice(evt.RoomID, "This is the relaybot management room. Send `!wa help` to get a list of commands.")
		mx.log.Debugln("Joined relaybot management room", evt.RoomID, "after invite from", evt.Sender)
		return
	}

	hasPuppets := false
	for mxid, _ := range members.Joined {
		if mxid == intent.UserID || mxid == evt.Sender {
			continue
		} else if _, ok := mx.bridge.ParsePuppetMXID(mxid); ok {
			hasPuppets = true
			continue
		}
		mx.log.Debugln("Leaving multi-user room", evt.RoomID, "after accepting invite from", evt.Sender)
		_, _ = intent.SendNotice(evt.RoomID, "This bridge is user-specific, please don't invite me into rooms with other users.")
		_, _ = intent.LeaveRoom(evt.RoomID)
		return
	}

	if !hasPuppets && (len(user.ManagementRoom) == 0 || evt.Content.AsMember().IsDirect) {
		user.SetManagementRoom(evt.RoomID)
		_, _ = intent.SendNotice(user.ManagementRoom, "This room has been registered as your bridge management/status room. Send `help` to get a list of commands.")
		mx.log.Debugln(evt.RoomID, "registered as a management room with", evt.Sender)
	}
}

func (mx *MatrixHandler) handlePrivatePortal(roomID id.RoomID, inviter *User, puppet *Puppet, key database.PortalKey) {
	portal := mx.bridge.GetPortalByJID(key)

	if len(portal.MXID) == 0 {
		mx.createPrivatePortalFromInvite(roomID, inviter, puppet, portal)
		return
	}

	err := portal.MainIntent().EnsureInvited(portal.MXID, inviter.MXID)
	if err != nil {
		mx.log.Warnfln("Failed to invite %s to existing private chat portal %s with %s: %v. Redirecting portal to new room...", inviter.MXID, portal.MXID, puppet.JID, err)
		mx.createPrivatePortalFromInvite(roomID, inviter, puppet, portal)
		return
	}
	intent := puppet.DefaultIntent()
	_, _ = intent.SendNotice(roomID, fmt.Sprintf("You already have a private chat portal with me at %s", roomID))
	mx.log.Debugln("Leaving private chat room", roomID, "as", puppet.MXID, "after accepting invite from", inviter.MXID, "as we already have chat with the user")
	_, _ = intent.LeaveRoom(roomID)
}


func (mx *MatrixHandler) createPrivatePortalFromInvite(roomID id.RoomID, inviter *User, puppet *Puppet, portal *Portal) {
	portal.MXID = roomID
	portal.Topic = "Skype private chat"
	_, _ = portal.MainIntent().SetRoomTopic(portal.MXID, portal.Topic)
	if portal.bridge.Config.Bridge.PrivateChatPortalMeta {
		portal.Name = puppet.Displayname
		portal.AvatarURL = puppet.AvatarURL
		portal.Avatar = puppet.Avatar
		_, _ = portal.MainIntent().SetRoomName(portal.MXID, portal.Name)
		_, _ = portal.MainIntent().SetRoomAvatar(portal.MXID, portal.AvatarURL)
	} else {
		portal.Name = ""
	}
	portal.log.Infoln("Created private chat portal in %s after invite from", roomID, inviter.MXID)
	intent := puppet.DefaultIntent()

	if mx.bridge.Config.Bridge.Encryption.Default {
		_, err := intent.InviteUser(roomID, &mautrix.ReqInviteUser{UserID: mx.bridge.Bot.UserID})
		if err != nil {
			portal.log.Warnln("Failed to invite bridge bot to enable e2be:", err)
		}
		err = mx.bridge.Bot.EnsureJoined(roomID)
		if err != nil {
			portal.log.Warnln("Failed to join as bridge bot to enable e2be:", err)
		}
		_, err = intent.SendStateEvent(roomID, event.StateEncryption, "", &event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1})
		if err != nil {
			portal.log.Warnln("Failed to enable e2be:", err)
		}
		mx.as.StateStore.SetMembership(roomID, inviter.MXID, event.MembershipJoin)
		mx.as.StateStore.SetMembership(roomID, puppet.MXID, event.MembershipJoin)
		mx.as.StateStore.SetMembership(roomID, mx.bridge.Bot.UserID, event.MembershipJoin)
		portal.Encrypted = true
	}
	portal.Update()
	portal.UpdateBridgeInfo()
	_, _ = intent.SendNotice(roomID, "Private chat portal created")

	err := portal.FillInitialHistory(inviter)
	if err != nil {
		portal.log.Errorln("Failed to fill history:", err)
	}

	inviter.addPortalToCommunity(portal)
	inviter.addPuppetToCommunity(puppet)
}

func (mx *MatrixHandler) HandlePuppetInvite(evt *event.Event, inviter *User, puppet *Puppet) {
	intent := puppet.DefaultIntent()
	members := mx.joinAndCheckMembers(evt, intent)
	if members == nil {
		return
	}
	var hasBridgeBot, hasOtherUsers bool
	for mxid, _ := range members.Joined {
		fmt.Println()
		fmt.Println()
		fmt.Println("HandlePuppetInvite mxid", mxid)
		fmt.Println("HandlePuppetInvite intent.UserID", intent.UserID)
		fmt.Println("HandlePuppetInvite patch.Parse(intent.UserID)", id.UserID(patch.Parse(string(intent.UserID))))
		fmt.Println("HandlePuppetInvite inviter.MXID", inviter.MXID)
		fmt.Println()
		fmt.Println()
		if mxid == id.UserID(patch.Parse(string(intent.UserID))) || mxid == inviter.MXID {
			continue
		} else if mxid == mx.bridge.Bot.UserID {
			hasBridgeBot = true
		} else {
			hasOtherUsers = true
		}
	}
	if !hasBridgeBot && !hasOtherUsers {
		key := database.NewPortalKey(puppet.JID, inviter.JID)
		mx.handlePrivatePortal(evt.RoomID, inviter, puppet, key)
	} else if !hasBridgeBot {
		mx.log.Debugln("Leaving multi-user room", evt.RoomID, "as", puppet.MXID, "after accepting invite from", evt.Sender)
		_, _ = intent.SendNotice(evt.RoomID, "Please invite the bridge bot first if you want to bridge to a skype group.")
		_, _ = intent.LeaveRoom(evt.RoomID)
	} else {
		_, _ = intent.SendNotice(evt.RoomID, "This puppet will remain inactive until this room is bridged to a Skype group.")
	}
}

func (mx *MatrixHandler) HandleMembership(evt *event.Event) {
	fmt.Println("HandleMembership0 evt.Sender:", evt.Sender)
	fmt.Println("HandleMembership0 evt.GetStateKey:", evt.GetStateKey())
	if _, isPuppet := mx.bridge.ParsePuppetMXID(evt.Sender); evt.Sender == mx.bridge.Bot.UserID || isPuppet {
		return
	}

	if mx.bridge.Crypto != nil {
		mx.bridge.Crypto.HandleMemberEvent(evt)
	}

	content := evt.Content.AsMember()
	if content.Membership == event.MembershipInvite && id.UserID(evt.GetStateKey()) == mx.as.BotMXID() {
		mx.HandleBotInvite(evt)
		return
	}

	user := mx.bridge.GetUserByMXID(evt.Sender)
	if user == nil || user.Conn == nil || user.Conn.LoginInfo == nil || !user.Whitelisted || !user.IsConnected() {
		return
	}

	portal := mx.bridge.GetPortalByMXID(evt.RoomID)
	if portal == nil {
		puppet := mx.bridge.GetPuppetByMXID(id.UserID(evt.GetStateKey()))
		if content.Membership == event.MembershipInvite && puppet != nil {
			mx.HandlePuppetInvite(evt, user, puppet)
		}
		return
	}
	isSelf := id.UserID(evt.GetStateKey()) == evt.Sender
	fmt.Println("HandleMembership isSelf:", isSelf)
	fmt.Println("HandleMembership id.UserID(evt.GetStateKey()):", id.UserID(evt.GetStateKey()))
	fmt.Println("HandleMembership evt.Sender:", evt.Sender)
	if content.Membership == event.MembershipLeave {
		if id.UserID(evt.GetStateKey()) == evt.Sender {
			if evt.Unsigned.PrevContent != nil {
				_ = evt.Unsigned.PrevContent.ParseRaw(evt.Type)
				prevContent, ok := evt.Unsigned.PrevContent.Parsed.(*event.MemberEventContent)
				if ok {
					if portal.IsPrivateChat() || prevContent.Membership == "join" {
						portal.HandleMatrixLeave(user)
					}
				}
			}
		} else {
			fmt.Println()
			fmt.Println()
			fmt.Println("HandleMembership evt.RoomID", evt.RoomID)
			fmt.Println("HandleMembership id.UserID(evt.GetStateKey())", id.UserID(evt.GetStateKey()))
			fmt.Println("HandleMembership event.MembershipLeave", event.MembershipLeave)
			fmt.Println("HandleMembership user.", event.MembershipLeave)
			fmt.Println()
			//mx.as.StateStore.SetMembership(evt.RoomID, id.UserID(evt.GetStateKey()), event.MembershipLeave)
			portal.HandleMatrixKick(user, evt)
		}
	} else if content.Membership == event.MembershipInvite && !isSelf {
		portal.HandleMatrixInvite(user, evt)
	}
}

func (mx *MatrixHandler) HandleRoomMetadata(evt *event.Event) {
	user := mx.bridge.GetUserByMXID(evt.Sender)
	if user == nil || !user.Whitelisted || !user.IsConnected() {
		return
	}

	portal := mx.bridge.GetPortalByMXID(evt.RoomID)
	if user.Conn == nil || user.Conn.LoginInfo == nil || portal == nil || portal.IsPrivateChat() {
		return
	}

	//var resp <-chan string
	var resp string
	var err error
	switch content := evt.Content.Parsed.(type) {
	case *event.RoomNameEventContent:
		resp, err = user.Conn.SetConversationThreads(portal.Key.JID, map[string]string{
			"topic": content.Name,
		})
	case *event.TopicEventContent:
		//resp, err = user.Conn.SetConversationThreads(portal.Key.JID, map[string]string{
		//	"topic": content.Topic,
		//})
		return
	case *event.RoomAvatarEventContent:
		data, err := portal.MainIntent().DownloadBytes(content.URL)
		if err != nil {
			portal.log.Errorfln("Failed to download media in %v", err)
			return
		}
		_, fileId, _, err := user.Conn.UploadFile(portal.Key.JID, &skype.SendMessage{
			Jid: portal.Key.JID,
			ClientMessageId: "",
			Type: "avatar/group",
			SendMediaMessage: &skype.SendMediaMessage{
				FileName: "avatar",
				RawData:  data,
				FileSize: strconv.Itoa(len(data)), // strconv.FormatUint(fileSize, 10),
				Duration: 0,
			},
		})
		if err != nil {
			mx.log.Errorln(err)
			return
		}
		resp, err = user.Conn.SetConversationThreads(portal.Key.JID, map[string]string{
			"picture": fmt.Sprintf("URL@https://api.asm.skype.com/v1/objects/%s", fileId),
		})
	}
	if err != nil {
		mx.log.Errorln(err)
	} else {
		//out := <-resp
		mx.log.Infoln(resp)
	}
}

func (mx *MatrixHandler) shouldIgnoreEvent(evt *event.Event) bool {
	if _, isPuppet := mx.bridge.ParsePuppetMXID(evt.Sender); evt.Sender == mx.bridge.Bot.UserID || isPuppet {
		fmt.Println()
		fmt.Printf("shouldIgnoreEvent: isPuppet%+v", isPuppet)
		fmt.Println()
		fmt.Printf("shouldIgnoreEvent: isPuppet%+v", evt.Sender)
		fmt.Println()
		return true
	}
	isCustomPuppet, ok := evt.Content.Raw["net.maunium.whatsapp.puppet"].(bool)
	if ok && isCustomPuppet && mx.bridge.GetPuppetByCustomMXID(evt.Sender) != nil {
		return true
	}
	user := mx.bridge.GetUserByMXID(evt.Sender)
	fmt.Println()
	fmt.Printf("shouldIgnoreEvent: user%+v", *user)
	fmt.Println()
	if !user.RelaybotWhitelisted {
		fmt.Println("user.RelaybotWhitelisted true", user.RelaybotWhitelisted)
		return true
	}
	fmt.Println("shouldIgnoreEvent: false")
	return false
}

func (mx *MatrixHandler) HandleEncrypted(evt *event.Event) {
	if mx.shouldIgnoreEvent(evt) || mx.bridge.Crypto == nil {
		fmt.Println("HandleEncrypted return 1")
		return
	}

	decrypted, err := mx.bridge.Crypto.Decrypt(evt)
	if err != nil {
		mx.log.Warnfln("Failed to decrypt %s: %v", evt.ID, err)
		return
	}
	mx.bridge.EventProcessor.Dispatch(decrypted)
}

func (mx *MatrixHandler) HandleMessage(evt *event.Event) {
	if mx.shouldIgnoreEvent(evt) {
		return
	}

	user := mx.bridge.GetUserByMXID(evt.Sender)
	content := evt.Content.AsMessage()
	if user.Whitelisted && content.MsgType == event.MsgText {
		commandPrefix := mx.bridge.Config.Bridge.CommandPrefix
		hasCommandPrefix := strings.HasPrefix(content.Body, commandPrefix)
		if hasCommandPrefix {
			content.Body = strings.TrimLeft(content.Body[len(commandPrefix):], " ")
		}
		if hasCommandPrefix || evt.RoomID == user.ManagementRoom {
			mx.cmd.Handle(evt.RoomID, user, content.Body)
			return
		}
	}
	fmt.Println()
	fmt.Printf("HandleMessage evt.RoomID1: %+v", evt.RoomID)
	fmt.Println()
	portal := mx.bridge.GetPortalByMXID(evt.RoomID)
	fmt.Println()
	fmt.Printf("HandleMessage portal: %+v", portal)
	fmt.Println()
	if user.Conn != nil && portal != nil && (user.Whitelisted || portal.HasRelaybot()) {
		portal.HandleMatrixMessage(user, evt)
	}
}

func (mx *MatrixHandler) HandleRedaction(evt *event.Event) {
	if _, isPuppet := mx.bridge.ParsePuppetMXID(evt.Sender); evt.Sender == mx.bridge.Bot.UserID || isPuppet {
		return
	}

	user := mx.bridge.GetUserByMXID(evt.Sender)

	if !user.Whitelisted {
		return
	}

	if !user.HasSession() {
		return
	} else if !user.IsConnected() {
		msg := format.RenderMarkdown(fmt.Sprintf("[%[1]s](https://matrix.to/#/%[1]s): \u26a0 "+
			"You are not connected to skype, so your redaction was not bridged. "+
			"Use `%[2]s reconnect` to reconnect.", user.MXID, mx.bridge.Config.Bridge.CommandPrefix), true, false)
		msg.MsgType = event.MsgNotice
		_, _ = mx.bridge.Bot.SendMessageEvent(evt.RoomID, event.EventMessage, msg)
		return
	}

	portal := mx.bridge.GetPortalByMXID(evt.RoomID)
	if user.Conn != nil && user.Conn.LoginInfo != nil && portal != nil {
		portal.HandleMatrixRedaction(user, evt)
	}
}
