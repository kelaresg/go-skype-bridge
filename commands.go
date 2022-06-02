package main

import (
	"errors"
	"fmt"
	"math"

	skype "github.com/kelaresg/go-skypeapi"
	"github.com/kelaresg/matrix-skype/database"
	skypeExt "github.com/kelaresg/matrix-skype/skype-ext"
	"maunium.net/go/mautrix/patch"

	"sort"
	"strconv"
	"strings"

	"maunium.net/go/maulogger/v2"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

type CommandHandler struct {
	bridge *Bridge
	log    maulogger.Logger
}

// NewCommandHandler creates a CommandHandler
func NewCommandHandler(bridge *Bridge) *CommandHandler {
	return &CommandHandler{
		bridge: bridge,
		log:    bridge.Log.Sub("Command handler"),
	}
}

// CommandEvent stores all data which might be used to handle commands
type CommandEvent struct {
	Bot     *appservice.IntentAPI
	Bridge  *Bridge
	Portal  *Portal
	Handler *CommandHandler
	RoomID  id.RoomID
	User    *User
	Command string
	Args    []string
}

// Reply sends a reply to command as notice
func (ce *CommandEvent) Reply(msg string, args ...interface{}) {
	content := format.RenderMarkdown(fmt.Sprintf(msg, args...), true, false)
	content.MsgType = event.MsgNotice
	intent := ce.Bot
	if ce.Portal != nil && ce.Portal.IsPrivateChat() {
		intent = ce.Portal.MainIntent()
	}
	_, err := intent.SendMessageEvent(ce.RoomID, event.EventMessage, content)
	if err != nil {
		ce.Handler.log.Warnfln("Failed to reply to command from %s: %v", ce.User.MXID, err)
	}
}

// Handle handles messages to the bridge
func (handler *CommandHandler) Handle(roomID id.RoomID, user *User, message string) {
	args := strings.Fields(message)
	ce := &CommandEvent{
		Bot:     handler.bridge.Bot,
		Bridge:  handler.bridge,
		Handler: handler,
		RoomID:  roomID,
		User:    user,
		Command: strings.ToLower(args[0]),
		Args:    args[1:],
	}
	if ce.Command == "login" {
		message = ""
	}
	handler.log.Debugfln("%s sent '%s' in %s", user.MXID, message, roomID)
	if roomID == handler.bridge.Config.Bridge.Relaybot.ManagementRoom {
		handler.CommandRelaybot(ce)
	} else {
		handler.CommandMux(ce)
	}
}

func (handler *CommandHandler) CommandMux(ce *CommandEvent) {
	switch ce.Command {
	case "relaybot":
		handler.CommandRelaybot(ce)
	case "login":
		handler.CommandLogin(ce)
	//case "logout-matrix":
	//	handler.CommandLogoutMatrix(ce)
	case "help":
		handler.CommandHelp(ce)
	//case "version":
	//	handler.CommandVersion(ce)
	case "ping":
		handler.CommandPing(ce)
	case "logout":
		handler.CommandLogout(ce)
	case "save-password":
		handler.CommandSavePassword(ce)
	case "remove-password":
		handler.CommandRemovePassword(ce)
	case "login-matrix", "sync", "list", "open", "pm", "invite", "kick", "leave", "join", "create", "share":
		if !ce.User.HasSession() {
			ce.Reply("You're not logged in. Use the `login` command to log into Skype.")
			return
		}

		switch ce.Command {
		//case "login-matrix":
		//	handler.CommandLoginMatrix(ce)
		case "sync":
			handler.CommandSync(ce)
		case "list":
			handler.CommandList(ce)
		case "open":
			handler.CommandOpen(ce)
		case "pm":
			handler.CommandPM(ce)
		case "invite":
			handler.CommandInvite(ce)
		case "kick":
			handler.CommandKick(ce)
		case "leave":
			handler.CommandLeave(ce)
		case "join":
			handler.CommandJoin(ce)
		case "share":
			handler.CommandShare(ce)
		case "create":
			handler.CommandCreate(ce)
		}
	default:
		handler.CommandSpecialMux(ce)
	}
}

func (handler *CommandHandler) CommandRelaybot(ce *CommandEvent) {
	if handler.bridge.Relaybot == nil {
		ce.Reply("The relaybot is disabled")
	} else if !ce.User.Admin {
		ce.Reply("Only admins can manage the relaybot")
	} else {
		if ce.Command == "relaybot" {
			if len(ce.Args) == 0 {
				ce.Reply("**Usage:** `relaybot <command>`")
				return
			}
			ce.Command = strings.ToLower(ce.Args[0])
			ce.Args = ce.Args[1:]
		}
		ce.User = handler.bridge.Relaybot
		handler.CommandMux(ce)
	}
}

func (handler *CommandHandler) CommandDevTest(_ *CommandEvent) {

}

const cmdVersionHelp = `version - View the bridge version`

func (handler *CommandHandler) CommandVersion(ce *CommandEvent) {
	version := fmt.Sprintf("v%s.unknown", Version)
	if Tag == Version {
		version = fmt.Sprintf("[v%s](%s/releases/v%s) (%s)", Version, URL, Tag, BuildTime)
	} else if len(Commit) > 8 {
		version = fmt.Sprintf("v%s.[%s](%s/commit/%s) (%s)", Version, Commit[:8], URL, Commit, BuildTime)
	}
	ce.Reply(fmt.Sprintf("[%s](%s) %s", Name, URL, version))
}

const cmdSetPowerLevelHelp = `set-pl [user ID] <power level> - Change the power level in a portal room. Only for bridge admins.`

func (handler *CommandHandler) CommandSetPowerLevel(ce *CommandEvent) {
	portal := ce.Bridge.GetPortalByMXID(ce.RoomID)
	if portal == nil {
		ce.Reply("Not a portal room")
		return
	}
	var level int
	var userID id.UserID
	var err error
	if len(ce.Args) == 1 {
		level, err = strconv.Atoi(ce.Args[0])
		if err != nil {
			ce.Reply("Invalid power level \"%s\"", ce.Args[0])
			return
		}
		userID = ce.User.MXID
	} else if len(ce.Args) == 2 {
		userID = id.UserID(ce.Args[0])
		_, _, err := userID.Parse()
		if err != nil {
			ce.Reply("Invalid user ID \"%s\"", ce.Args[0])
			return
		}
		level, err = strconv.Atoi(ce.Args[1])
		if err != nil {
			ce.Reply("Invalid power level \"%s\"", ce.Args[1])
			return
		}
	} else {
		ce.Reply("**Usage:** `set-pl [user] <level>`")
		return
	}
	intent := portal.MainIntent()
	_, err = intent.SetPowerLevel(ce.RoomID, userID, level)
	if err != nil {
		ce.Reply("Failed to set power levels: %v", err)
	}
}

const cmdLoginHelp = `login - login <_username_> <_password_>`

// CommandLogin handles login command
func (handler *CommandHandler) CommandLogin(ce *CommandEvent) {
	if len(ce.Args) == 0 {
		ce.Reply("**Usage:** `login username password`")
		return
	}
	if ce.User.Conn != nil && ce.User.Conn.LoggedIn == true {
		ce.Reply("You're already logged into Skype.")
		return
	}
	leavePortals(ce)
	if !ce.User.Connect(true) {
		ce.User.log.Debugln("Connect() returned false, assuming error was logged elsewhere and canceling login.")
		return
	}
	err := ce.User.Login(ce, ce.Args[0], ce.Args[1])
	if err == nil {
		syncAll(ce.User, true)
	}
}

const cmdLogoutHelp = `logout - Logout from Skype`

func (handler *CommandHandler) CommandLogout(ce *CommandEvent) {
	if ce.User.Conn == nil || ce.User.Conn.LoggedIn == false {
		ce.Reply("You're not logged into Skype.")
		return
	}
	//_ = ce.User.Conn.GetConversations("", ce.User.bridge.Config.Bridge.InitialChatSync)
	ce.User.Conn.LoggedIn = false
	username := ""
	password := ""
	if ce.User.Conn.LoginInfo != nil {
		username = ce.User.Conn.LoginInfo.Username
		password = ce.User.Conn.LoginInfo.Password
	}
	ce.User.Conn.LoginInfo = &skype.Session{
		SkypeToken:           "",
		SkypeExpires:         "",
		RegistrationToken:    "",
		RegistrationTokenStr: "",
		RegistrationExpires:  "",
		LocationHost:         "",
		EndpointId:           "",
		Username:             username,
		Password:             password,
	}

	ce.User.Conn.Store = &skype.Store{
		Contacts: make(map[string]skype.Contact),
		Chats:    make(map[string]skype.Conversation),
	}
	puppet := handler.bridge.GetPuppetByJID(ce.User.JID)
	if puppet.CustomMXID != "" {
		err := puppet.SwitchCustomMXID("", "")
		if err != nil {
			ce.User.log.Warnln("Failed to logout-matrix while logging out of WhatsApp:", err)
		}
	}
	ce.Reply("Logged out successfully.")
	ce.User.Conn.LoginInfo = nil
	if ce.User.Conn.Refresh != nil {
		ce.User.Conn.Refresh <- -1
	} else {
		leavePortals(ce)
	}
	ret := ce.User.bridge.DB.User.GetCredentialsByMXID(ce.User.MXID, &password, &username)
	if ret && password != "" {
		ce.Reply("WARNING, your password is stored in database. Use command `remove-password` to remove it.")
	}
}

const cmdSavePasswordHelp = `save-password - save user password into database`

// CommandSavePassword handles save-password command
func (handler *CommandHandler) CommandSavePassword(ce *CommandEvent) {
	var ret bool
	if len(ce.Args) > 0 {
		ce.Reply("**Usage:** `save-password`")
		return
	}

	if ce.User.Conn == nil || ce.User.Conn.LoggedIn == false {
		ce.Reply("You're not logged into Skype.")
		return
	}

	ret = ce.User.bridge.DB.User.SetCredentialsByMXID(ce.User.Conn.LoginInfo.Password, ce.User.Conn.LoginInfo.Username, ce.User.MXID)
	if ret == true {
		ce.Reply("Your password was successfully saved into database.")
	} else {
		ce.Reply("An error occurred while saving your password into database. Try it again.")
	}
}

const cmdRemovePasswordHelp = `remove-password - remove user password from database`

// CommandRemovePassword handles remove-password command
func (handler *CommandHandler) CommandRemovePassword(ce *CommandEvent) {
	var ret bool
	if len(ce.Args) > 0 {
		ce.Reply("**Usage:** `remove-password`")
		return
	}

	ret = ce.User.bridge.DB.User.SetCredentialsByMXID("", "", ce.User.MXID)
	if ret == true {
		ce.Reply("Your password was successfully removed from database.")
	} else {
		ce.Reply("An error occurred while removing your password from database. Try it again.")
	}
}

func leavePortals(ce *CommandEvent) {
	portals := ce.User.GetPortals()
	//newPortals := ce.User.GetPortalsNew()
	//allPortals := newPortals[0:]
	//for _, portal := range portals {
	//	var newPortalsHas bool
	//	for _, newPortal := range newPortals {
	//		if portal.Key == newPortal.Key {
	//			newPortalsHas = true
	//		}
	//	}
	//	if !newPortalsHas {
	//		allPortals = append(allPortals, portal)
	//	}
	//}

	leave := func(portal *Portal) {
		if len(portal.MXID) > 0 {
			_, _ = portal.MainIntent().KickUser(portal.MXID, &mautrix.ReqKickUser{
				Reason: "Logout",
				UserID: ce.User.MXID,
			})
		}
	}
	for _, portal := range portals {
		leave(portal)
	}
}

const cmdPingHelp = `ping - Check your connection to Skype.`

func (handler *CommandHandler) CommandPing(ce *CommandEvent) {
	if ce.User.Session == nil || ce.User.Session.SkypeToken == "" {
		if ce.User.IsLoginInProgress() {
			ce.Reply("You're not logged into Skype, but there's a login in progress.")
		} else {
			ce.Reply("You're not logged into Skype.")
		}
	} else if ce.User.Conn.LoggedIn == false {
		ce.Reply("You're not logged into Skype.")
	} else {
		username := ce.User.Conn.UserProfile.FirstName
		if len(ce.User.Conn.UserProfile.LastName) > 0 {
			username = username + ce.User.Conn.UserProfile.LastName
		}
		if username == "" {
			username = ce.User.Conn.UserProfile.Username
		}

		orgId := ""
		if patch.ThirdPartyIdEncrypt {
			orgId = patch.Enc(strings.TrimSuffix(ce.User.JID, skypeExt.NewUserSuffix))
		} else {
			orgId = strings.TrimSuffix(ce.User.JID, skypeExt.NewUserSuffix)
		}
		ce.Reply("You're logged in as @" + username + ", orgid is " + orgId)
	}
	var password string;
	var username string;
	ret := ce.User.bridge.DB.User.GetCredentialsByMXID(ce.User.MXID, &password, &username)
	if ret && password != "" {
		ce.Reply("WARNING, your password is stored in database. Use command `remove-password` to remove it.")
	}
}

const cmdHelpHelp = `help - Prints this help`

// CommandHelp handles help command
func (handler *CommandHandler) CommandHelp(ce *CommandEvent) {
	cmdPrefix := ""
	if ce.User.ManagementRoom != ce.RoomID || ce.User.IsRelaybot {
		cmdPrefix = handler.bridge.Config.Bridge.CommandPrefix + " "
	}

	ce.Reply("* " + strings.Join([]string{
		cmdPrefix + cmdHelpHelp,
		cmdPrefix + cmdLoginHelp,
		cmdPrefix + cmdLogoutHelp,
		cmdPrefix + cmdSavePasswordHelp,
		cmdPrefix + cmdRemovePasswordHelp,
		cmdPrefix + cmdPingHelp,
		//cmdPrefix + cmdLoginMatrixHelp,
		//cmdPrefix + cmdLogoutMatrixHelp,
		cmdPrefix + cmdSyncHelp,
		cmdPrefix + cmdListHelp,
		cmdPrefix + cmdOpenHelp,
		cmdPrefix + cmdPMHelp,
		cmdPrefix + cmdCreateHelp,
		cmdPrefix + cmdInviteHelp,
		cmdPrefix + cmdKickHelp,
		cmdPrefix + cmdLeaveHelp,
		cmdPrefix + cmdJoinHelp,
		cmdPrefix + cmdShareHelp,
	}, "\n* "))
}

const cmdSyncHelp = `sync - Synchronize contacts and optionally create portals for group chats.`

func (handler *CommandHandler) CommandSync(ce *CommandEvent) {
	user := ce.User
	create := len(ce.Args) > 0 && ce.Args[0] == "--create-all"
	ce.Reply("Updating contact and chat list...")
	handler.log.Debugln("Importing contacts of", user.MXID)

	ce.Reply("Syncing contacts...")
	err := user.Conn.Conn.ContactList(ce.User.Conn.UserProfile.Username)
	if err != nil {
		user.log.Errorln("Error get contacts:", err)
		ce.Reply("Failed to contacts chat list (see logs for details)")
	}
	ce.Reply("Syncing conversations...")
	err = ce.User.Conn.GetConversations("", user.bridge.Config.Bridge.InitialChatSync)
	if err != nil {
		user.log.Errorln("Error get conversations:", err)
		ce.Reply("Failed to conversations list (see logs for details)")
	}
	handler.log.Debugln("Importing chats of", user.MXID)
	syncAll(user, create)

	ce.Reply("Sync complete.")
}

func syncAll(user *User, create bool) {
	//ce.Reply("Syncing contacts...")
	user.syncPuppets(nil)
	//ce.Reply("Syncing chats...")
	user.syncPortals(nil, create)
	//sync information from non-contacts in the conversation，
	syncNonContactInfo(user)
}

func syncNonContactInfo(user *User) {
	nonContacts := map[string]skype.Contact{}
	for personId, contact := range user.Conn.Store.Contacts {
		if contact.PersonId == "" {
			nonContacts[personId] = contact
		}
	}
	user.syncPuppets(nonContacts)
}

const cmdDeletePortalHelp = `delete-portal - Delete the current portal. If the portal is used by other people, this is limited to bridge admins.`

func (handler *CommandHandler) CommandDeletePortal(ce *CommandEvent) {
	portal := ce.Bridge.GetPortalByMXID(ce.RoomID)
	if portal == nil {
		ce.Reply("You must be in a portal room to use that command")
		return
	}

	if !ce.User.Admin {
		users := portal.GetUserIDs()
		if len(users) > 1 || (len(users) == 1 && users[0] != ce.User.MXID) {
			ce.Reply("Only bridge admins can delete portals with other Matrix users")
			return
		}
	}

	portal.log.Infoln(ce.User.MXID, "requested deletion of portal.")
	portal.Delete()
	portal.Cleanup(false)
}

const cmdDeleteAllPortalsHelp = `delete-all-portals - Delete all your portals that aren't used by any other user.'`

func (handler *CommandHandler) CommandDeleteAllPortals(ce *CommandEvent) {
	portals := ce.User.GetPortals()
	portalsToDelete := make([]*Portal, 0, len(portals))
	for _, portal := range portals {
		users := portal.GetUserIDs()
		if len(users) == 1 && users[0] == ce.User.MXID {
			portalsToDelete = append(portalsToDelete, portal)
		}
	}
	leave := func(portal *Portal) {
		if len(portal.MXID) > 0 {
			_, _ = portal.MainIntent().KickUser(portal.MXID, &mautrix.ReqKickUser{
				Reason: "Deleting portal",
				UserID: ce.User.MXID,
			})
		}
	}
	customPuppet := handler.bridge.GetPuppetByCustomMXID(ce.User.MXID)
	if customPuppet != nil && customPuppet.CustomIntent() != nil {
		intent := customPuppet.CustomIntent()
		leave = func(portal *Portal) {
			if len(portal.MXID) > 0 {
				_, _ = intent.LeaveRoom(portal.MXID)
				_, _ = intent.ForgetRoom(portal.MXID)
			}
		}
	}
	ce.Reply("Found %d portals with no other users, deleting...", len(portalsToDelete))
	for _, portal := range portalsToDelete {
		portal.Delete()
		leave(portal)
	}
	ce.Reply("Finished deleting portal info. Now cleaning up rooms in background. " +
		"You may already continue using the bridge. Use `sync` to recreate portals.")

	go func() {
		for _, portal := range portalsToDelete {
			portal.Cleanup(false)
		}
		ce.Reply("Finished background cleanup of deleted portal rooms.")
	}()
}

const cmdListHelp = `list <_contacts|groups_> [page] [items per page] - Get a list of all contacts and groups.`

func formatLists(isContacts bool, contacts map[string]skype.Contact, conversations map[string]skype.Conversation) (result []string) {
	if isContacts {
		for _, contact := range contacts {
			result = append(result, fmt.Sprintf("* %s / %s - `%s`", contact.DisplayName, contact.DisplayNameSource, strings.Replace(contact.PersonId, skypeExt.NewUserSuffix, "", 1)))
		}
		sort.Sort(sort.StringSlice(result))
	} else {
		for _, conversation := range conversations {
			if strings.HasPrefix(fmt.Sprint(conversation.Id), "19:") {
				result = append(result, fmt.Sprintf("* %s - `%s`", conversation.ThreadProperties.Topic, conversation.Id))
			}
		}
		sort.Sort(sort.StringSlice(result))
	}
	return
}

func (handler *CommandHandler) CommandList(ce *CommandEvent) {
	if len(ce.Args) == 0 {
		ce.Reply("**Usage:** `list <contacts|groups> [page] [items per page]`")
		return
	}
	mode := strings.ToLower(ce.Args[0])
	if mode[0] != 'g' && mode[0] != 'c' {
		ce.Reply("**Usage:** `list <contacts|groups> [page] [items per page]`")
		return
	}

	var err error
	page := 1
	max := 100

	isContact := mode[0] == 'c'
	typeName := "Groups"
	if isContact {
		typeName = "Contacts"
		err = ce.User.Conn.ContactList(ce.User.Conn.UserProfile.Username)
		if err != nil {
			ce.Reply("Get Contacts error")
			return
		}
	} else {
		err = ce.User.Conn.GetConversations("", handler.bridge.Config.Bridge.InitialChatSync)
		if err != nil {
			ce.Reply("Get conversations error")
			return
		}
	}

	result := formatLists(isContact, ce.User.Conn.Store.Contacts, ce.User.Conn.Store.Chats)
	if len(result) == 0 {
		ce.Reply("No %s found", strings.ToLower(typeName))
		return
	}
	pages := int(math.Ceil(float64(len(result)) / float64(max)))
	if (page-1)*max >= len(result) {
		if pages == 1 {
			ce.Reply("There is only 1 page of %s", strings.ToLower(typeName))
		} else {
			ce.Reply("There are only %d pages of %s", pages, strings.ToLower(typeName))
		}
		return
	}
	lastIndex := page * max
	if lastIndex > len(result) {
		lastIndex = len(result)
	}
	result = result[(page-1)*max : lastIndex]
	ce.Reply("### %s (page %d of %d)\n\n%s", typeName, page, pages, strings.Join(result, "\n"))
}

const cmdOpenHelp = `open <_group ID_> - Open a group chat portal.`

func (handler *CommandHandler) CommandOpen(ce *CommandEvent) {
	if len(ce.Args) == 0 {
		ce.Reply("**Usage:** `open <group ID>`")
		return
	}

	user := ce.User
	jid := ce.Args[0]

	if strings.HasSuffix(jid, skypeExt.NewUserSuffix) {
		ce.Reply("That looks like a user ID. Did you mean `pm %s`?", jid[:len(jid)-len(skypeExt.NewUserSuffix)])
		return
	}
	ce.User.Conn.GetConversations("", handler.bridge.Config.Bridge.InitialChatSync)
	fmt.Println("user.Conn.Store.Chats: ", user.Conn.Store.Chats)
	chat, ok := user.Conn.Store.Chats[jid]
	if !ok {
		ce.Reply("Group ID not found in contacts. Try syncing contacts with `sync` first.")
		return
	}
	handler.log.Debugln("Importing", jid, "for", user)
	portal := user.bridge.GetPortalByJID(database.GroupPortalKey(jid))
	fmt.Println("CommandOpen portal.MXID", portal.MXID)
	if len(portal.MXID) > 0 {
		portal.SyncSkype(user, chat)
		ce.Reply("Portal room synced.")
	} else {
		portal.SyncSkype(user, chat)
		ce.Reply("Portal room created.")
	}
	_, _ = portal.MainIntent().InviteUser(portal.MXID, &mautrix.ReqInviteUser{UserID: user.MXID})

	//resp, err := ce.User.Conn.GetConsumptionHorizons(jid)
	//if err != nil {
	//	return
	//}
	//// var existIds []string
	//for _, con := range resp.ConsumptionHorizons {
	//	// existIds = append(existIds, strings.Replace(userId, skypeExt.NewUserSuffix, "", 1))
	//	has := false
	//	uId := ""
	//	for userId, _ := range ce.User.Conn.Store.Contacts {
	//		if con.Id == strings.Replace(userId, skypeExt.NewUserSuffix, "", 1) {
	//			has = true
	//		}
	//		uId = con.Id
	//	}
	//	if user.JID == con.Id + skypeExt.NewUserSuffix {
	//		continue
	//	}
	//	if !has && uId != "" {
	//		fmt.Println(fmt.Sprintf("https://avatar.skype.com/v1/avatars/%s/public?returnDefaultImage=false", uId))
	//		fmt.Println()
	//		newId := strings.Replace(con.Id, "8:", "", 1)
	//		avatar := &skypeExt.ProfilePicInfo{
	//			URL:    fmt.Sprintf("https://avatar.skype.com/v1/avatars/%s/public?returnDefaultImage=false", newId),
	//			Tag:    fmt.Sprintf("https://avatar.skype.com/v1/avatars/%s/public?returnDefaultImage=false", newId),
	//			Status: 0,
	//		}
	//		puppet := user.bridge.GetPuppetByJID(con.Id + skypeExt.NewUserSuffix)
	//		if puppet.Avatar != avatar.URL {
	//			puppet.UpdateAvatar(nil, avatar)
	//			_, err = ce.User.Conn.NameSearch(newId)
	//			if err != nil {
	//				ce.Reply("Failed to synchronize non-contact %s info", newId)
	//			}
	//		}
	//	}
	//}
	syncNonContactInfo(ce.User)
}

//func (handler *CommandHandler) CommandOpen(ce *CommandEvent) {
//	if len(ce.Args) == 0 {
//		ce.Reply("**Usage:** `open <group JID>`")
//		return
//	}
//
//	user := ce.User
//	jid := ce.Args[0]
//
//	if strings.HasSuffix(jid, whatsappExt.NewUserSuffix) {
//		ce.Reply("That looks like a user JID. Did you mean `pm %s`?", jid[:len(jid)-len(whatsappExt.NewUserSuffix)])
//		return
//	}
//
//	contact, ok := user.Conn.Store.Contacts[jid]
//	if !ok {
//		ce.Reply("Group JID not found in contacts. Try syncing contacts with `sync` first.")
//		return
//	}
//	handler.log.Debugln("Importing", jid, "for", user)
//	portal := user.bridge.GetPortalByJID(database.GroupPortalKey(jid))
//	if len(portal.MXID) > 0 {
//		portal.Sync(user, contact)
//		ce.Reply("Portal room synced.")
//	} else {
//		portal.Sync(user, contact)
//		ce.Reply("Portal room created.")
//	}
//	_, _ = portal.MainIntent().InviteUser(portal.MXID, &mautrix.ReqInviteUser{UserID: user.MXID})
//}

const cmdPMHelp = `pm <_user ID_> - Open a private chat with the given user id.`

func (handler *CommandHandler) CommandPM(ce *CommandEvent) {
	if len(ce.Args) == 0 {
		ce.Reply("**Usage:** `pm <user id>`")
		return
	}
	jid := ce.Args[0] + skypeExt.NewUserSuffix

	handler.log.Debugln("Importing", jid, "for", ce.User)

	contact, ok := ce.User.Conn.Store.Contacts[jid]
	if !ok {
		//if !force {
		ce.Reply("User id not found in contacts. Try syncing contacts with `sync` first. ")
		return
		//}
		//contact = skype.Contact{PersonId: jid}
	}

	puppet := ce.User.bridge.GetPuppetByJID(contact.PersonId)
	puppet.Sync(ce.User, contact)
	portal := ce.User.bridge.GetPortalByJID(database.NewPortalKey(ce.Args[0], ce.User.JID))
	fmt.Println("CommandPM user.JID", ce.User.JID)
	if len(portal.MXID) > 0 {
		_, err := portal.MainIntent().InviteUser(portal.MXID, &mautrix.ReqInviteUser{UserID: ce.User.MXID})
		if err != nil {
			fmt.Println(err)
		} else {
			ce.Reply("Existing portal room found, invited you to it.")
		}
		return
	}
	err := portal.CreateMatrixRoom(ce.User)
	if err != nil {
		ce.Reply("Failed to create portal room: %v", err)
		return
	}
	ce.Reply("Created portal room and invited you to it.")
}

//func (handler *CommandHandler) CommandPM(ce *CommandEvent) {
//	if len(ce.Args) == 0 {
//		ce.Reply("**Usage:** `pm [--force] <international phone number>`")
//		return
//	}
//
//	force := ce.Args[0] == "--force"
//	if force {
//		ce.Args = ce.Args[1:]
//	}
//
//	user := ce.User
//
//	number := strings.Join(ce.Args, "")
//	if number[0] == '+' {
//		number = number[1:]
//	}
//	for _, char := range number {
//		if char < '0' || char > '9' {
//			ce.Reply("Invalid phone number.")
//			return
//		}
//	}
//	jid := number + whatsappExt.NewUserSuffix
//
//	handler.log.Debugln("Importing", jid, "for", user)
//
//	contact, ok := user.Conn.Store.Contacts[jid]
//	if !ok {
//		if !force {
//			ce.Reply("Phone number not found in contacts. Try syncing contacts with `sync` first. " +
//				"To create a portal anyway, use `pm --force <number>`.")
//			return
//		}
//		contact = whatsapp.Contact{Jid: jid}
//	}
//	puppet := user.bridge.GetPuppetByJID(contact.Jid)
//	puppet.Sync(user, contact)
//	portal := user.bridge.GetPortalByJID(database.NewPortalKey(contact.Jid, user.JID))
//	if len(portal.MXID) > 0 {
//		_, err := portal.MainIntent().InviteUser(portal.MXID, &mautrix.ReqInviteUser{UserID: user.MXID})
//		if err != nil {
//			fmt.Println(err)
//		} else {
//			ce.Reply("Existing portal room found, invited you to it.")
//		}
//		return
//	}
//	err := portal.CreateMatrixRoom(user)
//	if err != nil {
//		ce.Reply("Failed to create portal room: %v", err)
//		return
//	}
//	ce.Reply("Created portal room and invited you to it.")
//}

const cmdLoginMatrixHelp = `login-matrix <_access token_> - Replace your WhatsApp account's Matrix puppet with your real Matrix account.'`

func (handler *CommandHandler) CommandLoginMatrix(ce *CommandEvent) {
	if len(ce.Args) == 0 {
		ce.Reply("**Usage:** `login-matrix <access token>`")
		return
	}
	puppet := handler.bridge.GetPuppetByJID(ce.User.JID)
	err := puppet.SwitchCustomMXID(ce.Args[0], ce.User.MXID)
	if err != nil {
		ce.Reply("Failed to switch puppet: %v", err)
		return
	}
	ce.Reply("Successfully switched puppet")
}

const cmdLogoutMatrixHelp = `logout-matrix - Switch your WhatsApp account's Matrix puppet back to the default one.`

func (handler *CommandHandler) CommandLogoutMatrix(ce *CommandEvent) {
	puppet := handler.bridge.GetPuppetByJID(ce.User.JID)
	if len(puppet.CustomMXID) == 0 {
		ce.Reply("You had not changed your WhatsApp account's Matrix puppet.")
		return
	}
	err := puppet.SwitchCustomMXID("", "")
	if err != nil {
		ce.Reply("Failed to remove custom puppet: %v", err)
		return
	}
	ce.Reply("Successfully removed custom puppet")
}

const cmdInviteHelp = `invite <_group ID_> <_contact id_>,... - Invite members to a group.`

func (handler *CommandHandler) CommandInvite(ce *CommandEvent) {
	if len(ce.Args) < 2 {
		ce.Reply("**Usage:** `invite <group JID> <contact id>,...`")
		return
	}
	user := ce.User
	conversationId := ce.Args[0]

	userNumbers := strings.Split(ce.Args[1], ",")

	if strings.HasSuffix(conversationId, skypeExt.NewUserSuffix) {
		ce.Reply("**Usage:** `invite <group JID> <contact id>,...`")
		return
	}

	_, ok := user.Conn.Store.Chats[conversationId]
	if !ok {
		//user.Conn
		err := ce.User.Conn.GetConversations("", handler.bridge.Config.Bridge.InitialChatSync)
		//time.Sleep(5 * time.Second)
		if err != nil {
			ce.Reply("get conversations failed. Try syncing contacts with `sync` first.")
		} else {
			_, ok = user.Conn.Store.Chats[conversationId]
			if !ok {
				ce.Reply("Group JID not found in chats. Try syncing groups with `sync` first.")
				return
			}
		}
	}
	handler.log.Debugln("GetConversations", conversationId, "for", user)
	handler.log.Debugln("Inviting", userNumbers, "to", conversationId)
	err := user.Conn.HandleGroupInvite(conversationId, userNumbers)
	if err != nil {
		ce.Reply("Please confirm that you have permission to invite members.")
	} else {
		ce.Reply("Group invitation sent.\nIf the member fails to join the group, please check your permissions or command parameters")
	}
}

const cmdKickHelp = `kick <_group ID_> <_contact Id>,... - Remove members from the group.`

func (handler *CommandHandler) CommandKick(ce *CommandEvent) {
	if len(ce.Args) < 2 {
		ce.Reply("**Usage:** `kick <group JID> <contact jid>,... reason`")
		return
	}

	user := ce.User
	converationId := ce.Args[0]
	userNumbers := strings.Split(ce.Args[1], ",")
	//reason := "omitempty"
	//if len(ce.Args) > 2 {
	//	reason = ce.Args[0]
	//}

	if strings.HasSuffix(converationId, skypeExt.NewUserSuffix) {
		ce.Reply("**Usage:** `kick <group ID> <contact id>,... reason`")
		return
	}

	//fmt.Println("user:", user)
	//fmt.Println("chats", user.Conn.Store.Chats)
	_, ok := user.Conn.Store.Chats[converationId]
	//fmt.Println("查找用户组是否存在：")
	//fmt.Println(group)
	if !ok {
		ce.Reply("Group ID not found in contacts. Try syncing contacts with `sync` first.")
		return
	}
	handler.log.Debugln("Importing", converationId, "for", user)
	portal := user.bridge.GetPortalByJID(database.GroupPortalKey(converationId))

	for i, number := range userNumbers {
		userNumbers[i] = number //  + skypeExt.NewUserSuffix
		member := portal.bridge.GetPuppetByJID(number + skypeExt.NewUserSuffix)

		if member == nil {
			portal.log.Errorln("%s is not a puppet", number)
			return
		}
	}

	handler.log.Debugln("Kicking", userNumbers, "to", converationId)
	err := user.Conn.HandleGroupKick(converationId, userNumbers)
	if err != nil {
		handler.log.Errorln("Kicking err", err)
		ce.Reply("Please confirm that you have permission to kick members.")
	} else {
		ce.Reply("Remove operation completed.\nIf the member has not been removed, please check your permissions or command parameters")
	}
}

const cmdLeaveHelp = `leave <_group ID_> - Leave a group.`

func (handler *CommandHandler) CommandLeave(ce *CommandEvent) {
	if len(ce.Args) == 0 {
		ce.Reply("**Usage:** `leave <group JID>`")
		return
	}

	user := ce.User
	groupId := ce.Args[0]

	if strings.HasSuffix(groupId, skypeExt.NewUserSuffix) {
		ce.Reply("**Usage:** `leave <group JID>`")
		return
	}
	//
	handler.log.Debugln("Importing", groupId, "for", user)
	portal := user.bridge.GetPortalByJID(database.GroupPortalKey(groupId))

	if len(portal.MXID) > 0 {
		cli := handler.bridge.Bot
		fmt.Println("cli appurl:", cli.Prefix, cli.AppServiceUserID, cli.HomeserverURL.String())
		//res, errLeave := cli.LeaveRoom(portal.MXID)
		//cli.AppServiceUserID = ce.User.MXID
		u := cli.BuildURL("rooms", portal.MXID, "leave")
		fmt.Println(u)
		resp := mautrix.RespLeaveRoom{}
		res, err := cli.MakeRequest("POST", u, struct{}{}, &resp)
		fmt.Println("leave res : ", res)
		fmt.Println("leave res err: ", err)
		//if errLeave != nil {
		//	portal.log.Errorln("Error leaving matrix room:", errLeave)
		//}
	}
	err := user.Conn.HandleGroupLeave(groupId)
	if err != nil {
		fmt.Println(err)
		ce.Reply("Leave operation failed.")
		return
	}
	ce.Reply("Leave operation completed and successful.")

}

const cmdShareHelp = `share <_group ID_> - Generate a link to join the group.`

func (handler *CommandHandler) CommandShare(ce *CommandEvent) {
	if len(ce.Args) == 0 {
		ce.Reply("**Usage:** `share <group id>`")
		return
	}
	user := ce.User
	converationId := ce.Args[0]
	fmt.Println("share converationId : ", converationId)
	//check the group is exists
	_, ok := user.Conn.Store.Chats[converationId]
	if !ok {
		ce.Reply("Group ID not found in groups. Try syncing groups with `sync` first.")
		return
	}
	//set enabled
	enstr := map[string]string{
		"joiningenabled": "true",
	}
	_, err := user.Conn.SetConversationThreads(converationId, enstr)
	if err != nil {
		ce.Reply("Set ConversationThreads failed.")
		return
	}
	//create share link
	err, link := user.Conn.HandleGroupShare(converationId)
	if err != nil {
		ce.Reply("Generate the share link failed.")
	}
	ce.Reply("The link ： " + link)

}

const cmdJoinHelp = `join <_invitation link_> - Join the group via the invitation link.`

func (handler *CommandHandler) CommandJoin(ce *CommandEvent) {
	if len(ce.Args) == 0 {
		ce.Reply("**Usage:** `join <Invitation link>`")
		return
	}

	user := ce.User
	joinurl := ce.Args[0]

	err, _ := user.Conn.HandleGroupJoin(joinurl)

	if err != nil {
		ce.Reply("Join group completed and failed.")
	}
	ce.Reply("Join group completed and successful.")
	//contact, ok := user.Conn.Store.Contacts[jid]
	//if !ok {
	//	ce.Reply("Group JID not found in contacts. Try syncing contacts with `sync` first.")
	//	return
	//}
	//handler.log.Debugln("Importing", jid, "for", user)
	//portal := user.bridge.GetPortalByJID(database.GroupPortalKey(jid))
	//if len(portal.MXID) > 0 {
	//	portal.Sync(user, contact)
	//	ce.Reply("Portal room synced.")
	//} else {
	//	portal.Sync(user, contact)
	//	ce.Reply("Portal room created.")
	//}
}

//
//func (handler *CommandHandler) CommandJoin(ce *CommandEvent) {
//	if len(ce.Args) == 0 {
//		ce.Reply("**Usage:** `join <Invitation link>`")
//		return
//	}
//
//	user := ce.User
//	params := strings.Split(ce.Args[0], "com/")
//
//	jid, err := user.Conn.HandleGroupJoin(params[len(params)-1])
//	if err == nil {
//		ce.Reply("Join operation completed.")
//	}
//
//	contact, ok := user.Conn.Store.Contacts[jid]
//	if !ok {
//		ce.Reply("Group JID not found in contacts. Try syncing contacts with `sync` first.")
//		return
//	}
//	handler.log.Debugln("Importing", jid, "for", user)
//	portal := user.bridge.GetPortalByJID(database.GroupPortalKey(jid))
//	if len(portal.MXID) > 0 {
//		portal.Sync(user, contact)
//		ce.Reply("Portal room synced.")
//	} else {
//		portal.Sync(user, contact)
//		ce.Reply("Portal room created.")
//	}
//}

const cmdCreateHelp = `create - Create a group chat.`

func (handler *CommandHandler) CommandCreate(ce *CommandEvent) {
	if ce.Portal != nil {
		ce.Reply("This is already a portal room")
		return
	}

	members, err := ce.Bot.JoinedMembers(ce.RoomID)
	handler.log.Debugln("Create Group-1", members)
	if err != nil {
		ce.Reply("Failed to get room members: %v", err)
		return
	}

	var roomNameEvent event.RoomNameEventContent
	err = ce.Bot.StateEvent(ce.RoomID, event.StateRoomName, "", &roomNameEvent)
	if err != nil && !errors.Is(err, mautrix.MNotFound) {
		ce.Reply("Failed to get room name")
		return
	} else if len(roomNameEvent.Name) == 0 {
		ce.Reply("Please set a name for the room first")
		return
	}

	var encryptionEvent event.EncryptionEventContent
	err = ce.Bot.StateEvent(ce.RoomID, event.StateEncryption, "", &encryptionEvent)
	if err != nil && !errors.Is(err, mautrix.MNotFound) {
		ce.Reply("Failed to get room encryption status")
		return
	}

	var participants []string
	for userID := range members.Joined {
		jid, ok := handler.bridge.ParsePuppetMXID(userID)
		if ok && jid != ce.User.JID {
			participants = append(participants, jid)
		}
	}

	selfMembers := skype.Members{}
	member2 := skype.Member{
		Id:   strings.Replace(ce.User.JID, skypeExt.NewUserSuffix, "", 1),
		Role: "Admin",
	}

	selfMembers.Members = append(selfMembers.Members, member2)
	selfMembers.Properties = skype.Properties{
		HistoryDisclosed: "true",
		Topic:            roomNameEvent.Name,
	}
	ce.User.currentCreateRoomName = roomNameEvent.Name
	handler.log.Debugln("Create Group", roomNameEvent.Name, "with", selfMembers, participants)
	err = ce.User.Conn.HandleGroupCreate(selfMembers)
	if err != nil {
		ce.Reply("Failed to create group: %v", err)
		return
	}
	participantMembers := skype.Members{}
	for _, participant := range participants {
		memberId := strings.Replace(participant, skypeExt.NewUserSuffix, "", 1)
		participantMembers.Members = append(participantMembers.Members, skype.Member{
			Id:   memberId,
			Role: "Admin",
		})
	}
	conversationId, ok := <-ce.User.Conn.CreateChan
	handler.log.Debugln("Create Group: conversationId=", conversationId)
	if ok {
		portal := handler.bridge.GetPortalByJID(database.GroupPortalKey(conversationId))
		portal.roomCreateLock.Lock()
		defer portal.roomCreateLock.Unlock()
		if len(portal.MXID) != 0 {
			portal.log.Warnln("Detected race condition in room creation")
			// TODO race condition, clean up the old room
		}
		portal.MXID = ce.RoomID
		portal.Name = roomNameEvent.Name
		portal.Encrypted = encryptionEvent.Algorithm == id.AlgorithmMegolmV1
		if !portal.Encrypted && handler.bridge.Config.Bridge.Encryption.Default {
			_, err = portal.MainIntent().SendStateEvent(portal.MXID, event.StateEncryption, "", &event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1})
			if err != nil {
				portal.log.Warnln("Failed to enable e2be:", err)
			}
			portal.Encrypted = true
		}
		handler.log.Debugln("Create Group: before update protal")
		portal.Update()
		portal.UpdateBridgeInfo()
		ce.User.currentCreateRoomName = ""
		handler.log.Debugln("Create Group: after update protal")

		err = ce.User.Conn.AddMember(participantMembers, conversationId)
		ce.Reply("Successfully created Skype group %s", portal.Key.JID)
	}

	//ce.User.addPortalToCommunity(portal)
}
