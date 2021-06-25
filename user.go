package main

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	skype "github.com/kelaresg/go-skypeapi"
	skypeExt "github.com/kelaresg/matrix-skype/skype-ext"
	"maunium.net/go/mautrix/patch"
	"sort"
	//"strconv"
	"strings"
	"sync"
	"time"

	log "maunium.net/go/maulogger/v2"
	"maunium.net/go/mautrix"

	//"github.com/Rhymen/go-whatsapp"
	//waProto "github.com/Rhymen/go-whatsapp/binary/proto"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/kelaresg/matrix-skype/database"
	"github.com/kelaresg/matrix-skype/types"
	//"github.com/kelaresg/matrix-skype/whatsapp-ext"
)

type User struct {
	*database.User
	Conn *skypeExt.ExtendedConn

	bridge *Bridge
	log    log.Logger

	Admin               bool
	Whitelisted         bool
	RelaybotWhitelisted bool

	IsRelaybot bool

	ConnectionErrors int
	CommunityID      string

	cleanDisconnection bool

	chatListReceived chan struct{}
	syncPortalsDone  chan struct{}

	messages chan PortalMessage
	syncLock sync.Mutex

	mgmtCreateLock sync.Mutex

	contactsPresence map[string]*skypeExt.Presence
}

func (bridge *Bridge) GetUserByMXID(userID id.UserID) *User {
	_, isPuppet := bridge.ParsePuppetMXID(userID)
	fmt.Println("GetUserByMXID0", userID)
	fmt.Println("GetUserByMXID1", bridge.Bot.UserID)
	if isPuppet || userID == bridge.Bot.UserID {
		fmt.Println("GetUserByMXID2", userID)
		fmt.Println("GetUserByMXID3", bridge.Bot.UserID)
		return nil
	}
	bridge.usersLock.Lock()
	defer bridge.usersLock.Unlock()
	user, ok := bridge.usersByMXID[userID]
	if !ok {
		return bridge.loadDBUser(bridge.DB.User.GetByMXID(userID), &userID)
	}
	return user
}

func (bridge *Bridge) GetUserByJID(userID types.SkypeID) *User {
	bridge.usersLock.Lock()
	defer bridge.usersLock.Unlock()
	user, ok := bridge.usersByJID[userID]
	if !ok {
		return bridge.loadDBUser(bridge.DB.User.GetByJID(userID), nil)
	}
	return user
}

func (user *User) getSkypeIdByMixId() (skypeId string){
	mixIdArr := strings.Split(string(user.MXID), "&")
	idArr := strings.Split(mixIdArr[1], ":"+user.bridge.Config.Homeserver.Domain)
	skypeId = strings.Replace(idArr[0], "-",":",2)
	return skypeId
}

func (user *User) addToJIDMap() {
	user.bridge.usersLock.Lock()
	user.bridge.usersByJID[user.JID] = user
	user.bridge.usersLock.Unlock()
}

func (user *User) removeFromJIDMap() {
	user.bridge.usersLock.Lock()
	delete(user.bridge.usersByJID, user.JID)
	user.bridge.usersLock.Unlock()
}

func (bridge *Bridge) GetAllUsers() []*User {
	bridge.usersLock.Lock()
	defer bridge.usersLock.Unlock()
	dbUsers := bridge.DB.User.GetAll()
	output := make([]*User, len(dbUsers))
	for index, dbUser := range dbUsers {
		user, ok := bridge.usersByMXID[dbUser.MXID]
		if !ok {
			user = bridge.loadDBUser(dbUser, nil)
		}
		user.contactsPresence = make(map[string]*skypeExt.Presence)
		output[index] = user
	}
	return output
}

func (bridge *Bridge) loadDBUser(dbUser *database.User, mxid *id.UserID) *User {
	if dbUser == nil {
		if mxid == nil {
			return nil
		}
		dbUser = bridge.DB.User.New()
		dbUser.MXID = *mxid
		dbUser.Insert()
	}
	user := bridge.NewUser(dbUser)
	bridge.usersByMXID[user.MXID] = user
	if len(user.JID) > 0 {
		bridge.usersByJID[user.JID] = user
	}
	if len(user.ManagementRoom) > 0 {
		bridge.managementRooms[user.ManagementRoom] = user
	}
	return user
}

func (user *User) GetPortals() []*Portal {
	keys := user.User.GetPortalKeys()
	portals := make([]*Portal, len(keys))

	user.bridge.portalsLock.Lock()
	for i, key := range keys {
		portal, ok := user.bridge.portalsByJID[key]
		if !ok {
			portal = user.bridge.loadDBPortal(user.bridge.DB.Portal.GetByJID(key), &key)
		}
		portals[i] = portal
	}
	user.bridge.portalsLock.Unlock()
	return portals
}

func (user *User) GetPortalsNew() []*Portal {
	keys := make([]database.PortalKey, len(user.Conn.Store.Chats))
	i := 0
	for jid, _ := range user.Conn.Store.Chats {
		keys[i] = database.NewPortalKey(jid, user.JID)
		i++
	}

	portals := make([]*Portal, len(keys))

	user.bridge.portalsLock.Lock()
	for i, key := range keys {
		portal, ok := user.bridge.portalsByJID[key]
		if !ok {
			portal = user.bridge.loadDBPortal(user.bridge.DB.Portal.GetByJID(key), &key)
		}
		portals[i] = portal
	}
	user.bridge.portalsLock.Unlock()
	return portals
}

func (bridge *Bridge) NewUser(dbUser *database.User) *User {
	user := &User{
		User:   dbUser,
		bridge: bridge,
		log:    bridge.Log.Sub("User").Sub(string(dbUser.MXID)),

		IsRelaybot: false,

		chatListReceived: make(chan struct{}, 1),
		syncPortalsDone:  make(chan struct{}, 1),
		messages:         make(chan PortalMessage, 256),
	}
	user.RelaybotWhitelisted = user.bridge.Config.Bridge.Permissions.IsRelaybotWhitelisted(user.MXID)
	user.Whitelisted = user.bridge.Config.Bridge.Permissions.IsWhitelisted(user.MXID)
	user.Admin = user.bridge.Config.Bridge.Permissions.IsAdmin(user.MXID)
	go user.handleMessageLoop()
	return user
}

func (user *User) GetManagementRoom() id.RoomID {
	if len(user.ManagementRoom) == 0 {
		user.mgmtCreateLock.Lock()
		defer user.mgmtCreateLock.Unlock()
		if len(user.ManagementRoom) > 0 {
			return user.ManagementRoom
		}
		resp, err := user.bridge.Bot.CreateRoom(&mautrix.ReqCreateRoom{
			Topic:    "Skype bridge notices",
			IsDirect: true,
		})
		if err != nil {
			user.log.Errorln("Failed to auto-create management room:", err)
		} else {
			user.SetManagementRoom(resp.RoomID)
		}
	}
	return user.ManagementRoom
}

func (user *User) SetManagementRoom(roomID id.RoomID) {
	existingUser, ok := user.bridge.managementRooms[roomID]
	if ok {
		existingUser.ManagementRoom = ""
		existingUser.Update()
	}

	user.ManagementRoom = roomID
	user.bridge.managementRooms[user.ManagementRoom] = user
	user.Update()
}

func (user *User) SetSession(session *skype.Session) {
	user.Session = session
	if session == nil {
		user.LastConnection = 0
	}
	user.Update()
}

//func (user *User) Connect(evenIfNoSession bool) bool {
//	if user.Conn != nil {
//		return true
//	} else if !evenIfNoSession && user.Session == nil {
//		return false
//	}
//	user.log.Debugln("Connecting to WhatsApp")
//	timeout := time.Duration(user.bridge.Config.Bridge.ConnectionTimeout)
//	if timeout == 0 {
//		timeout = 20
//	}
//	conn, err := whatsapp.NewConn(timeout * time.Second)
//	if err != nil {
//		user.log.Errorln("Failed to connect to WhatsApp:", err)
//		msg := format.RenderMarkdown("\u26a0 Failed to connect to WhatsApp server. "+
//			"This indicates a network problem on the bridge server. See bridge logs for more info.", true, false)
//		_, _ = user.bridge.Bot.SendMessageEvent(user.GetManagementRoom(), event.EventMessage, msg)
//		return false
//	}
//	user.Conn = whatsappExt.ExtendConn(conn)
//	_ = user.Conn.SetClientName("matrix-skype bridge", "mx-wa", SkypeVersion)
//	user.log.Debugln("WhatsApp connection successful")
//	user.Conn.AddHandler(user)
//	return user.RestoreSession()
//}

func (user *User) Connect(evenIfNoSession bool) bool {
	if user.Conn != nil {
		return true
	} else if !evenIfNoSession && user.Session == nil {
		return false
	}
	user.log.Debugln("Connecting to skype")
	timeout := time.Duration(user.bridge.Config.Bridge.ConnectionTimeout)
	if timeout == 0 {
		timeout = 20
	}
	conn, err := skype.NewConn()
	if err != nil {
		user.log.Errorln("Failed to connect to skype:", err)
		//msg := format.RenderMarkdown("\u26a0 Failed to connect to WhatsApp server. "+
		//	"This indicates a network problem on the bridge server. See bridge logs for more info.", true, false)
		//_, _ = user.bridge.Bot.SendMessageEvent(user.GetManagementRoom(), event.EventMessage, msg)
		return false
	}
	user.Conn = skypeExt.ExtendConn(conn)
	//_ = user.Conn.SetClientName("matrix-skype bridge", "mx-wa", SkypeVersion)
	user.log.Debugln("skype connection successful")
	user.Conn.AddHandler(user)

	return user.RestoreSession()
}

func (user *User) RestoreSession() bool {
	if user.Session != nil {
		//sess, err := user.Conn.RestoreWithSession(*user.Session)
		//if err == whatsapp.ErrAlreadyLoggedIn {
		//	return true
		//} else if err != nil {
		//	user.log.Errorln("Failed to restore session:", err)
		//	msg := format.RenderMarkdown("\u26a0 Failed to connect to WhatsApp. Make sure WhatsApp "+
		//		"on your phone is reachable and use `reconnect` to try connecting again.", true, false)
		//	_, _ = user.bridge.Bot.SendMessageEvent(user.GetManagementRoom(), event.EventMessage, msg)
		//	user.log.Debugln("Disconnecting due to failed session restore...")
		//	_, err := user.Conn.Disconnect()
		//	if err != nil {
		//		user.log.Errorln("Failed to disconnect after failed session restore:", err)
		//	}
		//	return false
		//}
		//user.ConnectionErrors = 0
		//user.SetSession(&sess)
		//user.log.Debugln("Session restored successfully")
		//user.PostLogin()
	}
	return true
}

func (user *User) HasSession() bool {
	//return user.Session != nil
	return true
}

func (user *User) IsConnected() bool {
	//return user.Conn != nil && user.Conn.IsConnected() && user.Conn.IsLoggedIn()
	return true
}

func (user *User) IsLoginInProgress() bool {
	return user.Conn != nil && user.Conn.IsLoginInProgress()
}

func (user *User) Login(ce *CommandEvent, name string, password string) (err error) {
	if user.contactsPresence == nil {
		user.contactsPresence = make(map[string]*skypeExt.Presence)
	}
	err = user.Conn.Login(name, password)
	if err != nil {
		user.log.Errorln("Failed to login:", err)
		orgId := ""
		if patch.ThirdPartyIdEncrypt {
			orgId = patch.Enc(strings.TrimSuffix(user.JID, skypeExt.NewUserSuffix))
		} else {
			orgId = strings.TrimSuffix(user.JID, skypeExt.NewUserSuffix)
		}
		ce.Reply(err.Error() + ", orgid is " + orgId)
		return err
	}
	username := user.Conn.UserProfile.FirstName
	if len(user.Conn.UserProfile.LastName) > 0 {
		username = username + user.Conn.UserProfile.LastName
	}
	if username == "" {
		username = user.Conn.UserProfile.Username
	}

	orgId := ""
	if patch.ThirdPartyIdEncrypt {
		orgId = patch.Enc(strings.TrimSuffix(user.JID, skypeExt.NewUserSuffix))
	} else {
		orgId = strings.TrimSuffix(user.JID, skypeExt.NewUserSuffix)
	}
	ce.Reply("Successfully logged in as @" + username + ", orgid is " + orgId)

	user.Conn.Subscribes() // subscribe basic event
	err = user.Conn.Conn.ContactList(user.Conn.UserProfile.Username)
	if err == nil{
		var userIds []string
		for _, contact := range user.Conn.Store.Contacts {
			if strings.Index(contact.PersonId, "28:") > -1 {
				continue
			}
			userId := strings.Replace(contact.PersonId, skypeExt.NewUserSuffix, "", 1)
			userIds = append(userIds, userId)
		}
		ce.User.Conn.SubscribeUsers(userIds)
		go loopPresence(user)
	}
	go user.Conn.Poll()
	go user.monitorSession(ce)

	user.ConnectionErrors = 0
	user.JID = "8:" + user.Conn.UserProfile.Username + skypeExt.NewUserSuffix
	user.addToJIDMap()
	user.SetSession(user.Conn.LoginInfo)
	_ = ce.User.Conn.GetConversations("", user.bridge.Config.Bridge.InitialChatSync)
	user.PostLogin()
	return
}

func (user *User) monitorSession(ce *CommandEvent) {
	user.Conn.Refresh = make(chan int)
	for x := range user.Conn.Refresh {
		fmt.Println("monitorSession: ", x)
		if x > 0 {
			user.SetSession(user.Conn.LoginInfo)
		} else {
			ce.Reply("Session expired")
			close(user.Conn.Refresh)
			leavePortals(ce)
		}
	}

	item, ok := <- user.Conn.Refresh
	if !ok {
		user.Conn.Refresh = nil
	}
	fmt.Println("monitorSession1", item, ok)
}

func loopPresence(user *User) {
	for {
		for cid, contact := range user.contactsPresence {
			puppet := user.bridge.GetPuppetByJID(cid)
			_ = puppet.DefaultIntent().SetPresence(event.Presence(strings.ToLower(contact.Availability)))
		}
		time.Sleep(39 * time.Second)
	}
}

type Chat struct {
	Portal          *Portal
	LastMessageTime uint64
	Contact         skype.Conversation
}

type ChatList []Chat

func (cl ChatList) Len() int {
	return len(cl)
}

func (cl ChatList) Less(i, j int) bool {
	return cl[i].LastMessageTime > cl[j].LastMessageTime
}

func (cl ChatList) Swap(i, j int) {
	cl[i], cl[j] = cl[j], cl[i]
}

func (user *User) PostLogin() {
	user.log.Debugln("Locking processing of incoming messages and starting post-login sync")
	user.syncLock.Lock()
	go user.intPostLogin()
}

func (user *User) tryAutomaticDoublePuppeting() {
	fmt.Println("tryAutomaticDoublePuppeting")
	if len(user.bridge.Config.Bridge.LoginSharedSecret) == 0 {
		fmt.Println("tryAutomaticDoublePuppeting-1", user.bridge.Config.Bridge.LoginSharedSecret)
		// Automatic login not enabled
		return
	} else if _, homeserver, _ := user.MXID.Parse(); homeserver != user.bridge.Config.Homeserver.Domain {
		fmt.Println("tryAutomaticDoublePuppeting-2", user.MXID)
		fmt.Println("tryAutomaticDoublePuppeting-21", homeserver)
		fmt.Println("tryAutomaticDoublePuppeting--3", user.bridge.Config.Homeserver.Domain)
		// user is on another homeserver
		return
	}
	fmt.Println("tryAutomaticDoublePuppeting1")
	puppet := user.bridge.GetPuppetByJID(user.JID)
	if len(puppet.CustomMXID) > 0 {
		// Custom puppet already enabled
		return
	}
	fmt.Println("tryAutomaticDoublePuppeting2", user.MXID)
	_,_ = user.UpdateAccessToken(puppet)
}

func (user *User) UpdateAccessToken(puppet *Puppet) (err error, accessToken string) {
	if len(user.bridge.Config.Bridge.LoginSharedSecret) == 0 {
		return errors.New("you didn't set LoginSharedSecret"), ""
	} else if _, homeserver, _ := user.MXID.Parse(); homeserver != user.bridge.Config.Homeserver.Domain {
		// user is on another homeserver
		return errors.New("user is on another homeServer"), ""
	}
	accessToken, err = puppet.loginWithSharedSecret(user.MXID)
	if err != nil {
		user.log.Warnln("Failed to login with shared secret:", err)
		return
	}
	err = puppet.SwitchCustomMXID(accessToken, user.MXID)
	if err != nil {
		puppet.log.Warnln("Failed to switch to auto-logined custom puppet:", err)
		return
	}
	user.log.Infoln("Successfully automatically enabled custom puppet")
	return
}

func (user *User) intPostLogin() {
	defer user.syncLock.Unlock()
	user.createCommunity()
	user.tryAutomaticDoublePuppeting()

	select {
	case <-user.chatListReceived:
		user.log.Debugln("Chat list receive confirmation received in PostLogin")
	case <-time.After(time.Duration(user.bridge.Config.Bridge.ChatListWait) * time.Second):
		user.log.Warnln("Timed out waiting for chat list to arrive! Unlocking processing of incoming messages.")
		return
	}
	select {
	case <-user.syncPortalsDone:
		user.log.Debugln("Post-login portal sync complete, unlocking processing of incoming messages.")
	case <-time.After(time.Duration(user.bridge.Config.Bridge.PortalSyncWait) * time.Second):
		user.log.Warnln("Timed out waiting for chat list to arrive! Unlocking processing of incoming messages.")
	}
}

func (user *User) syncPortals(chatMap map[string]skype.Conversation, createAll bool) {
	if chatMap == nil {
		chatMap = user.Conn.Store.Chats
	}
	user.log.Infoln("Reading chat list")
	chats := make(ChatList, 0, len(chatMap))
	existingKeys := user.GetInCommunityMap()
	portalKeys := make([]database.PortalKeyWithMeta, 0, len(chatMap))
	for _, chat := range chatMap {
		t, err := time.Parse(time.RFC3339,chat.LastMessage.ComposeTime)
		if err != nil {
			t = time.Now()
			if chat.Properties.ConversationStatus != "Accepted" && len(chat.ThreadProperties.Lastjoinat) < 1 {
				continue
			}
		}
		// Filter calllogs conversation
		if chat.Id == "48:calllogs" {
			continue
		}
		// Filter starred(bookmarks)
		if chat.Id == "48:starred" {
			continue
		}
		// Filter conversations that have not sent messages
		if chat.LastMessage.Id == "" {
			continue
		}
		// 'Lastleaveat' value means that you have left the current conversation
		if len(chat.ThreadProperties.Lastleaveat) > 0 {
			continue
		}
		ts := uint64(t.UnixNano())
		cid, _ := chat.Id.(string)
		portal := user.GetPortalByJID(cid)

		chats = append(chats, Chat{
			Portal:          portal,
			Contact:         user.Conn.Store.Chats[cid],
			LastMessageTime: ts,
		})
		var inCommunity, ok bool
		if inCommunity, ok = existingKeys[portal.Key]; !ok || !inCommunity {
			inCommunity = user.addPortalToCommunity(portal)
			if portal.IsPrivateChat() {
				puppet := user.bridge.GetPuppetByJID(portal.Key.JID + skypeExt.NewUserSuffix)
				user.addPuppetToCommunity(puppet)
			}
		}
		portalKeys = append(portalKeys, database.PortalKeyWithMeta{PortalKey: portal.Key, InCommunity: inCommunity})
	}
	user.log.Infoln("Read chat list, updating user-portal mapping")
	err := user.SetPortalKeys(portalKeys)
	if err != nil {
		user.log.Warnln("Failed to update user-portal mapping:", err)
	}
	sort.Sort(chats)
	limit := user.bridge.Config.Bridge.InitialChatSync
	if limit < 0 {
		limit = len(chats)
	}
	now := uint64(time.Now().Unix())
	user.log.Infoln("Syncing portals")
	for i, chat := range chats {
		if chat.LastMessageTime+user.bridge.Config.Bridge.SyncChatMaxAge < now {
			break
		}
		create := (chat.LastMessageTime >= user.LastConnection && user.LastConnection > 0) || i < limit
		if len(chat.Portal.MXID) > 0 || create || createAll {
			chat.Portal.SyncSkype(user, chat.Contact)
			//err := chat.Portal.BackfillHistory(user, chat.LastMessageTime)
			if err != nil {
				chat.Portal.log.Errorln("Error backfilling history:", err)
			}
		}
	}
	user.log.Infoln("Finished syncing portals")
	select {
	case user.syncPortalsDone <- struct{}{}:
	default:
	}
}

//func (user *User) HandleContactList(contacts []whatsapp.Contact) {
//	contactMap := make(map[string]whatsapp.Contact)
//	for _, contact := range contacts {
//		contactMap[contact.Jid] = contact
//	}
//	// go user.syncPuppets(contactMap)
//}

func (user *User) syncPuppets(contacts map[string]skype.Contact) {
	if contacts == nil {
		contacts = user.Conn.Store.Contacts
	}
	if len(contacts) < 1 {
		user.log.Infoln("No contacts to sync")
		return
	}
	user.log.Infoln("Syncing puppet info from contacts")
	//for jid, contact := range contacts {
	username := user.Conn.UserProfile.FirstName
	if user.Conn.UserProfile.LastName != "" {
		username = user.Conn.UserProfile.FirstName + " " + user.Conn.UserProfile.LastName
	}
	contacts["8:" + user.Conn.UserProfile.Username + skypeExt.NewUserSuffix] = skype.Contact{
		Profile: skype.UserInfoProfile{
			AvatarUrl: user.Conn.UserProfile.AvatarUrl,
		},
		DisplayName: username,
		PersonId: user.Conn.UserProfile.Username,
	}
	for personId, contact := range contacts {
		fmt.Println("Syncing puppet info from contacts", personId)
		user.log.Infoln("Syncing puppet info from contacts", strings.HasSuffix(personId, skypeExt.NewUserSuffix))
		fmt.Println("Syncing puppet info from contacts", skypeExt.NewUserSuffix)
		if strings.HasSuffix(personId, skypeExt.NewUserSuffix) {
			fmt.Println("Syncing puppet info from contacts i am coming")
			puppet := user.bridge.GetPuppetByJID(personId)
			puppet.Sync(user, contact)
		}
	}
	user.log.Infoln("Finished syncing puppet info from contacts")
}

func (user *User) updateLastConnectionIfNecessary() {
	if user.LastConnection+60 < uint64(time.Now().Unix()) {
		user.UpdateLastConnection()
	}
}

func (user *User) HandleError(err error) {
	// Otherwise unknown error, probably mostly harmless
}

func (user *User) tryReconnect(msg string) {
	//if user.ConnectionErrors > user.bridge.Config.Bridge.MaxConnectionAttempts {
	//	content := format.RenderMarkdown(fmt.Sprintf("%s. Use the `reconnect` command to reconnect.", msg), true, false)
	//	_, _ = user.bridge.Bot.SendMessageEvent(user.GetManagementRoom(), event.EventMessage, content)
	//	return
	//}
	//if user.bridge.Config.Bridge.ReportConnectionRetry {
	//	_, _ = user.bridge.Bot.SendNotice(user.GetManagementRoom(), fmt.Sprintf("%s. Reconnecting...", msg))
	//	// Don't want the same error to be repeated
	//	msg = ""
	//}
	//var tries uint
	//var exponentialBackoff bool
	//baseDelay := time.Duration(user.bridge.Config.Bridge.ConnectionRetryDelay)
	//if baseDelay < 0 {
	//	exponentialBackoff = true
	//	baseDelay = -baseDelay + 1
	//}
	//delay := baseDelay
	//for user.ConnectionErrors <= user.bridge.Config.Bridge.MaxConnectionAttempts {
	//	err := user.Conn.Restore()
	//	if err == nil {
	//		user.ConnectionErrors = 0
	//		if user.bridge.Config.Bridge.ReportConnectionRetry {
	//			_, _ = user.bridge.Bot.SendNotice(user.GetManagementRoom(), "Reconnected successfully")
	//		}
	//		user.PostLogin()
	//		return
	//	} else if err.Error() == "init responded with 400" {
	//		user.log.Infoln("Got init 400 error when trying to reconnect, resetting connection...")
	//		sess, err := user.Conn.Disconnect()
	//		if err != nil {
	//			user.log.Debugln("Error while disconnecting for connection reset:", err)
	//		}
	//		if len(sess.Wid) > 0 {
	//			user.SetSession(&sess)
	//		}
	//	}
	//	user.log.Errorln("Error while trying to reconnect after disconnection:", err)
	//	tries++
	//	user.ConnectionErrors++
	//	if user.ConnectionErrors <= user.bridge.Config.Bridge.MaxConnectionAttempts {
	//		if exponentialBackoff {
	//			delay = (1 << tries) + baseDelay
	//		}
	//		if user.bridge.Config.Bridge.ReportConnectionRetry {
	//			_, _ = user.bridge.Bot.SendNotice(user.GetManagementRoom(),
	//				fmt.Sprintf("Reconnection attempt failed: %v. Retrying in %d seconds...", err, delay))
	//		}
	//		time.Sleep(delay * time.Second)
	//	}
	//}
	//
	//if user.bridge.Config.Bridge.ReportConnectionRetry {
	//	msg = fmt.Sprintf("%d reconnection attempts failed. Use the `reconnect` command to try to reconnect manually.", tries)
	//} else {
	//	msg = fmt.Sprintf("\u26a0 %sAdditionally, %d reconnection attempts failed. "+
	//		"Use the `reconnect` command to try to reconnect.", msg, tries)
	//}
	//
	//content := format.RenderMarkdown(msg, true, false)
	//_, _ = user.bridge.Bot.SendMessageEvent(user.GetManagementRoom(), event.EventMessage, content)
}

func (user *User) ShouldCallSynchronously() bool {
	return true
}

func (user *User) HandleJSONParseError(err error) {
	user.log.Errorln("WhatsApp JSON parse error:", err)
}

func (user *User) PortalKey(jid types.SkypeID) database.PortalKey {
	fmt.Println("User PortalKey jid: ", jid)
	fmt.Println("User PortalKey  user.JID: ",  user.JID)
	return database.NewPortalKey(jid, user.JID)
}

func (user *User) GetPortalByJID(jid types.SkypeID) *Portal {
	return user.bridge.GetPortalByJID(user.PortalKey(jid))
}

func (user *User) handleMessageLoop() {
	for msg := range user.messages {
		user.syncLock.Lock()
		fmt.Println("User handleMessageLoop msg.chat: ", msg.chat)
		user.GetPortalByJID(msg.chat).messages <- msg
		user.syncLock.Unlock()
	}
}

func (user *User) putMessage(message PortalMessage) {
	select {
	case user.messages <- message:
	default:
		user.log.Warnln("Buffer is full, dropping message in", message.chat)
	}
}

func (user *User) HandleTextMessage(message skype.Resource) {
	user.log.Debugf("HandleTextMessage: ", message)
	user.putMessage(PortalMessage{message.Jid, user, message, uint64(message.Timestamp)})
}

func (user *User) HandleImageMessage(message skype.Resource) {
	user.log.Debugf("HandleImageMessage: ", message)
	user.putMessage(PortalMessage{message.Jid, user, message, uint64(message.Timestamp)})
}

//func (user *User) HandleStickerMessage(message whatsapp.StickerMessage) {
//	user.putMessage(PortalMessage{message.Info.RemoteJid, user, message, message.Info.Timestamp})
//}
//
//func (user *User) HandleVideoMessage(message whatsapp.VideoMessage) {
//	user.putMessage(PortalMessage{message.Info.RemoteJid, user, message, message.Info.Timestamp})
//}
//
//func (user *User) HandleAudioMessage(message whatsapp.AudioMessage) {
//	user.putMessage(PortalMessage{message.Info.RemoteJid, user, message, message.Info.Timestamp})
//}
//
//func (user *User) HandleDocumentMessage(message whatsapp.DocumentMessage) {
//	user.putMessage(PortalMessage{message.Info.RemoteJid, user, message, message.Info.Timestamp})
//}

func (user *User) HandleContactMessage(message skype.Resource) {
	user.log.Debugf("HandleContactMessage: ", message)
	user.putMessage(PortalMessage{message.Jid, user, message, uint64(message.Timestamp)})
}

func (user *User) HandleLocationMessage(message skype.Resource) {
	user.log.Debugf("HandleLocationMessage: ", message)
	user.putMessage(PortalMessage{message.Jid, user, message, uint64(message.Timestamp)})
}

func (user *User) HandleMessageRevoke(message skype.Resource) {
	user.putMessage(PortalMessage{message.Jid, user, message, uint64(message.Timestamp)})
}

type FakeMessage struct {
	Text  string
	ID    string
	Alert bool
}

//func (user *User) HandleCallInfo(info whatsappExt.CallInfo) {
//	if info.Data != nil {
//		return
//	}
//	data := FakeMessage{
//		ID: info.ID,
//	}
//	switch info.Type {
//	case whatsappExt.CallOffer:
//		if !user.bridge.Config.Bridge.CallNotices.Start {
//			return
//		}
//		data.Text = "Incoming call"
//		data.Alert = true
//	case whatsappExt.CallOfferVideo:
//		if !user.bridge.Config.Bridge.CallNotices.Start {
//			return
//		}
//		data.Text = "Incoming video call"
//		data.Alert = true
//	case whatsappExt.CallTerminate:
//		if !user.bridge.Config.Bridge.CallNotices.End {
//			return
//		}
//		data.Text = "Call ended"
//		data.ID += "E"
//	default:
//		return
//	}
//	portal := user.GetPortalByJID(info.From)
//	if portal != nil {
//		portal.messages <- PortalMessage{info.From, user, data, 0}
//	}
//}

func (user *User) HandleTypingStatus(info skype.Resource) {
	sendId := info.SendId + skypeExt.NewUserSuffix
	puppet := user.bridge.GetPuppetByJID(sendId)

	switch info.MessageType {
	case "Control/ClearTyping":
		if len(puppet.typingIn) > 0 && puppet.typingAt+15 > time.Now().Unix() {
			portal := user.bridge.GetPortalByMXID(puppet.typingIn)
			_, _ = puppet.IntentFor(portal).UserTyping(puppet.typingIn, false, 0)
			puppet.typingIn = ""
			puppet.typingAt = 0
		}
	case "Control/Typing":
		portal := user.GetPortalByJID(info.Jid)
		if len(puppet.typingIn) > 0 && puppet.typingAt+15 > time.Now().Unix() {
			if puppet.typingIn == portal.MXID {
				return
			}
			_, _ = puppet.IntentFor(portal).UserTyping(puppet.typingIn, false, 0)
		}
		puppet.typingIn = portal.MXID
		puppet.typingAt = time.Now().Unix()
		_, _ = puppet.IntentFor(portal).UserTyping(portal.MXID, true, 10*1000)
		time.Sleep(10 * time.Second)
		_, _ = puppet.IntentFor(portal).UserTyping(portal.MXID, false, 0)
		//_ = puppet.DefaultIntent().SetPresence("online")
	}
}

func (user *User) HandlePresence(info skype.Resource) {
	sendId := info.SendId + skypeExt.NewUserSuffix
	puppet := user.bridge.GetPuppetByJID(sendId)

	if _,ok := user.contactsPresence[sendId]; ok {
		user.contactsPresence[sendId].Availability = info.Availability
		user.contactsPresence[sendId].Status = info.Status
	} else {
		user.contactsPresence[sendId] = &skypeExt.Presence {
			Id: sendId,
			Availability: info.Availability,
			Status: info.Status,
		}
	}

	switch skype.Presence(info.Availability) {
	case skype.PresenceOffline:
		_ = puppet.DefaultIntent().SetPresence("offline")
	case skype.PresenceOnline:
		if len(puppet.typingIn) > 0 && puppet.typingAt+15 > time.Now().Unix() {
			portal := user.bridge.GetPortalByMXID(puppet.typingIn)
			_, _ = puppet.IntentFor(portal).UserTyping(puppet.typingIn, false, 0)
			puppet.typingIn = ""
			puppet.typingAt = 0
		} else {
			 _ = puppet.DefaultIntent().SetPresence("online")
		}
	//case whatsapp.PresenceComposing:
	//	portal := user.GetPortalByJID(info.Jid)
	//	if len(puppet.typingIn) > 0 && puppet.typingAt+15 > time.Now().Unix() {
	//		if puppet.typingIn == portal.MXID {
	//			return
	//		}
	//		_, _ = puppet.IntentFor(portal).UserTyping(puppet.typingIn, false, 0)
	//	}
	//	puppet.typingIn = portal.MXID
	//	puppet.typingAt = time.Now().Unix()
	//	_, _ = puppet.IntentFor(portal).UserTyping(portal.MXID, true, 15*1000)
	//	_ = puppet.DefaultIntent().SetPresence("online")
	}
}

//func (user *User) HandlePresenceWA(info whatsappExt.Presence) {
//	puppet := user.bridge.GetPuppetByJID(info.SenderJID)
//	switch info.Status {
//	case whatsapp.PresenceUnavailable:
//		_ = puppet.DefaultIntent().SetPresence("offline")
//	case whatsapp.PresenceAvailable:
//		if len(puppet.typingIn) > 0 && puppet.typingAt+15 > time.Now().Unix() {
//			portal := user.bridge.GetPortalByMXID(puppet.typingIn)
//			_, _ = puppet.IntentFor(portal).UserTyping(puppet.typingIn, false, 0)
//			puppet.typingIn = ""
//			puppet.typingAt = 0
//		}
//		_ = puppet.DefaultIntent().SetPresence("online")
//	case whatsapp.PresenceComposing:
//		portal := user.GetPortalByJID(info.JID)
//		if len(puppet.typingIn) > 0 && puppet.typingAt+15 > time.Now().Unix() {
//			if puppet.typingIn == portal.MXID {
//				return
//			}
//			_, _ = puppet.IntentFor(portal).UserTyping(puppet.typingIn, false, 0)
//		}
//		puppet.typingIn = portal.MXID
//		puppet.typingAt = time.Now().Unix()
//		_, _ = puppet.IntentFor(portal).UserTyping(portal.MXID, true, 15*1000)
//		_ = puppet.DefaultIntent().SetPresence("online")
//	}
//}

//func (user *User) HandleMsgInfo(info whatsappExt.MsgInfo) {
//	if (info.Command == whatsappExt.MsgInfoCommandAck || info.Command == whatsappExt.MsgInfoCommandAcks) && info.Acknowledgement == whatsappExt.AckMessageRead {
//		portal := user.GetPortalByJID(info.ToJID)
//		if len(portal.MXID) == 0 {
//			return
//		}
//
//		go func() {
//			intent := user.bridge.GetPuppetByJID(info.SenderJID).IntentFor(portal)
//			for _, id := range info.IDs {
//				msg := user.bridge.DB.Message.GetByJID(portal.Key, id)
//				if msg == nil {
//					continue
//				}
//
//				err := intent.MarkRead(portal.MXID, msg.MXID)
//				if err != nil {
//					user.log.Warnln("Failed to mark message %s as read by %s: %v", msg.MXID, info.SenderJID, err)
//				}
//			}
//		}()
//	}
//}

func (user *User) HandleCommand(cmd skypeExt.Command) {
	switch cmd.Type {
	case skypeExt.CommandPicture:
		if strings.HasSuffix(cmd.JID, skypeExt.NewUserSuffix) {
			puppet := user.bridge.GetPuppetByJID(cmd.JID)
			go puppet.UpdateAvatar(user, cmd.ProfilePicInfo)
		} else {
			portal := user.GetPortalByJID(cmd.JID)
			go portal.UpdateAvatar(user, cmd.ProfilePicInfo)
		}
	case skypeExt.CommandDisconnect:
		//var msg string
		//if cmd.Kind == "replaced" {
		//	msg = "\u26a0 Your Skype connection was closed by the server because you opened another Skype Web client.\n\n" +
		//		"Use the `reconnect` command to disconnect the other client and resume bridging."
		//} else {
		//	user.log.Warnln("Unknown kind of disconnect:", string(cmd.Raw))
		//	msg = fmt.Sprintf("\u26a0 Your Skype connection was closed by the server (reason code: %s).\n\n"+
		//		"Use the `reconnect` command to reconnect.", cmd.Kind)
		//}
		//msg = "can not disconnect"
		//user.cleanDisconnection = true
		//go user.bridge.Bot.SendMessageEvent(user.GetManagementRoom(), event.EventMessage, format.RenderMarkdown(msg, true, false))
	}
}

func (user *User) HandleChatUpdate(cmd skype.Resource) {
	fmt.Println()
	fmt.Println("HandleChatUpdate :")
	fmt.Println()
	//if cmd.Command != skypeExt.ChatUpdateCommandAction {
	//	return
	//}

	portal := user.GetPortalByJID(cmd.Jid)
	//if len(portal.MXID) == 0 {
	//	if cmd.Data.Action == skypeExt.ChatActionCreate {
	//		go portal.membershipCreate(user, cmd)
	//	}
	//	return
	//}

	fmt.Println("portl:", portal)
	fmt.Println("cmd.MessageType:", cmd.MessageType)
	messageType := skypeExt.ChatActionType(cmd.MessageType)

	switch messageType {
	case skypeExt.ChatTopicUpdate:
		topicContent := skype.ChatTopicContent{}
		//把xml数据解析成bs对象
		xml.Unmarshal([]byte(cmd.Content), &topicContent)
		portalName := ""
		noRoomTopic := false
		names := strings.Split(cmd.ThreadTopic, ", ")
		for _, name := range names {
			key := "8:" + name + skypeExt.NewUserSuffix
			if key == user.JID {
				noRoomTopic = true
			}
		}
		if noRoomTopic {
			participants, _ := portal.GetPuppets()
			for index, participant := range participants {
				if *participant.DisplayName != user.Conn.LoginInfo.Username {
					if len(portalName) == 0 {
						portalName = *participant.DisplayName
					} else {
						if index > 5 {
							portalName = portalName + ", ..."
							break
						} else {
							portalName = *participant.DisplayName + ", " + portalName
						}
					}
				}
			}
		} else {
			portalName = cmd.ThreadTopic
		}
		cmd.SendId = topicContent.Initiator + skypeExt.NewUserSuffix
		go portal.UpdateName(portalName, cmd.SendId)
	case skypeExt.ChatPictureUpdate:
		topicContent := skype.ChatPictureContent{}
		//把xml数据解析成bs对象
		xml.Unmarshal([]byte(cmd.Content), &topicContent)
		cmd.SendId = topicContent.Initiator + skypeExt.NewUserSuffix
		url := strings.TrimPrefix(topicContent.Value, "URL@")
		if strings.Index(url, "/views/") > 0 {
			url = strings.Replace(url, "avatar_fullsize", "swx_avatar", 1)
		} else {
			url = url + "/views/swx_avatar"
		}
		fmt.Println()
		fmt.Println("HandleChatUpdateL picture:", url )
		fmt.Println()
		avatar := &skypeExt.ProfilePicInfo{
			URL:    url,
			Tag:    url,
			Status: 0,
		}
		go portal.UpdateAvatar(user, avatar)
	case skypeExt.ChatMemberAdd:
		user.log.Debugfln("chat member add")
		if len(portal.MXID) == 0 {
			err := portal.CreateMatrixRoom(user)
			if err != nil {
				fmt.Println("create room failed")
			}
		}
		go portal.membershipAdd(cmd.Content)
	case skypeExt.ChatMemberDelete:
		go portal.membershipRemove(cmd.Content)
	case "":
		if skypeExt.ChatActionType(cmd.Type) == skypeExt.ChatActionThread {
			if len(cmd.ETag) > 0 &&  len(cmd.Properties.Capabilities) < 1{
				portal.Delete()
				portal.Cleanup(false)
			}
		}

	//case skypeExt.ChatActionAddTopic:
	//	go portal.UpdateTopic(cmd.Data.AddTopic.Topic, cmd.Data.SenderJID)
	//case skypeExt.ChatActionRemoveTopic:
	//	go portal.UpdateTopic("", cmd.Data.SenderJID)
	//case skypeExt.ChatActionPromote:
	//	go portal.ChangeAdminStatus(cmd.Data.PermissionChange.JIDs, true)
	//case skypeExt.ChatActionDemote:
	//	go portal.ChangeAdminStatus(cmd.Data.PermissionChange.JIDs, false)
	//case skypeExt.ChatActionAnnounce:
	//	go portal.RestrictMessageSending(cmd.Data.Announce)
	//case skypeExt.ChatActionRestrict:
	//	go portal.RestrictMetadataChanges(cmd.Data.Restrict)
	//case skypeExt.ChatActionAdd:
	//	go portal.membershipAdd(user, cmd.Jid)
	//case skypeExt.ChatActionRemove:
	//	go portal.membershipRemove(cmd.Data.MemberAction.JIDs, cmd.Data.Action)
	//case skypeExt.ChatActionIntroduce:
	//	go portal.membershipAdd(user, cmd.JID)
	}
}


//func (user *User) HandleChatUpdate(cmd whatsappExt.ChatUpdate) {
//	if cmd.Command != whatsappExt.ChatUpdateCommandAction {
//		return
//	}
//
//	portal := user.GetPortalByJID(cmd.JID)
//	if len(portal.MXID) == 0 {
//		if cmd.Data.Action == whatsappExt.ChatActionCreate {
//			go portal.membershipCreate(user, cmd)
//		}
//		return
//	}
//
//	switch cmd.Data.Action {
//	case whatsappExt.ChatActionNameChange:
//		go portal.UpdateName(cmd.Data.NameChange.Name, cmd.Data.SenderJID)
//	case whatsappExt.ChatActionAddTopic:
//		go portal.UpdateTopic(cmd.Data.AddTopic.Topic, cmd.Data.SenderJID)
//	case whatsappExt.ChatActionRemoveTopic:
//		go portal.UpdateTopic("", cmd.Data.SenderJID)
//	case whatsappExt.ChatActionPromote:
//		go portal.ChangeAdminStatus(cmd.Data.PermissionChange.JIDs, true)
//	case whatsappExt.ChatActionDemote:
//		go portal.ChangeAdminStatus(cmd.Data.PermissionChange.JIDs, false)
//	case whatsappExt.ChatActionAnnounce:
//		go portal.RestrictMessageSending(cmd.Data.Announce)
//	case whatsappExt.ChatActionRestrict:
//		go portal.RestrictMetadataChanges(cmd.Data.Restrict)
//	case whatsappExt.ChatActionAdd:
//		go portal.membershipAdd(user, cmd.JID)
//	case whatsappExt.ChatActionRemove:
//		go portal.membershipRemove(cmd.Data.MemberAction.JIDs, cmd.Data.Action)
//	case whatsappExt.ChatActionIntroduce:
//		go portal.membershipAdd(user, cmd.JID)
//	}
//}

func (user *User) HandleJsonMessage(message string) {
	var msg json.RawMessage
	err := json.Unmarshal([]byte(message), &msg)
	if err != nil {
		return
	}
	user.log.Debugln("JSON message:", message)
	user.updateLastConnectionIfNecessary()
}

//func (user *User) HandleRawMessage(message *waProto.WebMessageInfo) {
//	user.updateLastConnectionIfNecessary()
//}

func (user *User) NeedsRelaybot(portal *Portal) bool {
	return false
	//return !user.HasSession() || !user.IsInPortal(portal.Key)
}
