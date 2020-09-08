package main

import (
	"fmt"
	skype "github.com/kelaresg/go-skypeapi"
	"strconv"
	"strings"

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

func (mx *MatrixHandler) HandleBotInvite(evt *event.Event) {
	intent := mx.as.BotIntent()

	user := mx.bridge.GetUserByMXID(evt.Sender)
	if user == nil {
		return
	}

	resp, err := intent.JoinRoomByID(evt.RoomID)
	if err != nil {
		mx.log.Debugln("Failed to join room", evt.RoomID, "with invite from", evt.Sender)
		return
	}

	members, err := intent.JoinedMembers(resp.RoomID)
	if err != nil {
		mx.log.Debugln("Failed to get members in room", resp.RoomID, "after accepting invite from", evt.Sender)
		intent.LeaveRoom(resp.RoomID)
		return
	}

	if len(members.Joined) < 2 {
		mx.log.Debugln("Leaving empty room", resp.RoomID, "after accepting invite from", evt.Sender)
		intent.LeaveRoom(resp.RoomID)
		return
	}

	if !user.Whitelisted {
		intent.SendNotice(resp.RoomID, "You are not whitelisted to use this bridge.\n"+
			"If you're the owner of this bridge, see the bridge.permissions section in your config file.")
		intent.LeaveRoom(resp.RoomID)
		return
	}

	if evt.RoomID == mx.bridge.Config.Bridge.Relaybot.ManagementRoom {
		intent.SendNotice(evt.RoomID, "This is the relaybot management room. Send `!wa help` to get a list of commands.")
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
		mx.log.Debugln("Leaving multi-user room", resp.RoomID, "after accepting invite from", evt.Sender)
		intent.SendNotice(resp.RoomID, "This bridge is user-specific, please don't invite me into rooms with other users.")
		intent.LeaveRoom(resp.RoomID)
		return
	}

	if !hasPuppets {
		user := mx.bridge.GetUserByMXID(evt.Sender)
		user.SetManagementRoom(resp.RoomID)
		intent.SendNotice(user.ManagementRoom, "This room has been registered as your bridge management/status room. Send `help` to get a list of commands.")
		mx.log.Debugln(resp.RoomID, "registered as a management room with", evt.Sender)
	}
}

func (mx *MatrixHandler) HandleMembership(evt *event.Event) {
	if _, isPuppet := mx.bridge.ParsePuppetMXID(evt.Sender); evt.Sender == mx.bridge.Bot.UserID || isPuppet {
		return
	}

	if mx.bridge.Crypto != nil {
		mx.bridge.Crypto.HandleMemberEvent(evt)
	}

	content := evt.Content.AsMember()
	if content.Membership == event.MembershipInvite && id.UserID(evt.GetStateKey()) == mx.as.BotMXID() {
		mx.HandleBotInvite(evt)
	}

	portal := mx.bridge.GetPortalByMXID(evt.RoomID)
	if portal == nil {
		return
	}

	user := mx.bridge.GetUserByMXID(evt.Sender)
	if user == nil || !user.Whitelisted || !user.IsConnected() {
		return
	}

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
			portal.HandleMatrixKick(user, evt)
		}
	}
}

func (mx *MatrixHandler) HandleRoomMetadata(evt *event.Event) {
	user := mx.bridge.GetUserByMXID(evt.Sender)
	if user == nil || !user.Whitelisted || !user.IsConnected() {
		return
	}

	portal := mx.bridge.GetPortalByMXID(evt.RoomID)
	if portal == nil || portal.IsPrivateChat() {
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
	fmt.Println()
	fmt.Printf("HandleMessage : %+v", evt)
	fmt.Println()
	if mx.shouldIgnoreEvent(evt) {
		return
	}

	user := mx.bridge.GetUserByMXID(evt.Sender)
	fmt.Println()
	fmt.Printf("HandleMessage user: %+v", user)
	fmt.Println()
	content := evt.Content.AsMessage()
	if user.Whitelisted && content.MsgType == event.MsgText {
		commandPrefix := mx.bridge.Config.Bridge.CommandPrefix
		fmt.Println()
		fmt.Printf("HandleMessage commandPrefix: %+v", commandPrefix)
		fmt.Println()
		hasCommandPrefix := strings.HasPrefix(content.Body, commandPrefix)
		if hasCommandPrefix {
			content.Body = strings.TrimLeft(content.Body[len(commandPrefix):], " ")
		}
		fmt.Println()
		fmt.Printf("HandleMessage hasCommandPrefix: %+v", hasCommandPrefix)
		fmt.Println()
		fmt.Println()
		fmt.Printf("HandleMessage  evt.RoomID0: %+v", evt.RoomID)
		fmt.Println()
		fmt.Println()
		fmt.Printf("HandleMessage  user.ManagementRoom: %+v", user.ManagementRoom)
		fmt.Println()
		if hasCommandPrefix || evt.RoomID == user.ManagementRoom {
			fmt.Println()
			fmt.Printf("HandleMessage commandPrefix: %+v", commandPrefix)
			fmt.Println()
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
	if portal != nil && (user.Whitelisted || portal.HasRelaybot()) {
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
	if portal != nil {
		portal.HandleMatrixRedaction(user, evt)
	}
}
