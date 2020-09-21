package main

import (
	"fmt"
	skype "github.com/kelaresg/go-skypeapi"
	skypeExt "github.com/kelaresg/matrix-skype/skype-ext"
	"net/http"
	"regexp"
	"strings"

	log "maunium.net/go/maulogger/v2"

	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/id"

	"github.com/kelaresg/matrix-skype/database"
	"github.com/kelaresg/matrix-skype/types"
	"github.com/kelaresg/matrix-skype/whatsapp-ext"
)

func (bridge *Bridge) ParsePuppetMXID(mxid id.UserID) (types.SkypeID, bool) {
	userIDRegex, err := regexp.Compile(fmt.Sprintf("^@%s:%s$",
		bridge.Config.Bridge.FormatUsername("(.*)"),
		bridge.Config.Homeserver.Domain))
	if err != nil {
		bridge.Log.Warnln("Failed to compile puppet user ID regex:", err)
		return "", false
	}
	match := userIDRegex.FindStringSubmatch(string(mxid))
	if match == nil || len(match) != 2 {
		return "", false
	}
	realId := match[1]
	cond1 := "8-live-"
	cond2 := "8-"
	if strings.HasPrefix(realId, cond1) {
		realId = strings.Replace(realId, cond1, "8:live:", 1)
	} else if strings.HasPrefix(realId, cond2){
		realId = strings.Replace(realId, cond2, "8:", 1)
	}
	jid := types.SkypeID(realId + skypeExt.NewUserSuffix)
	return jid, true
}

func (bridge *Bridge) GetPuppetByMXID(mxid id.UserID) *Puppet {
	jid, ok := bridge.ParsePuppetMXID(mxid)
	if !ok {
		return nil
	}

	return bridge.GetPuppetByJID(jid)
}

func (bridge *Bridge) GetPuppetByJID(jid types.SkypeID) *Puppet {
	jid = strings.Trim(jid, " ")
	if len(jid) < 1 {
		return nil
	}
	if strings.Index(jid, skypeExt.NewUserSuffix) < 0 {
		jid = jid + skypeExt.NewUserSuffix
	}
	bridge.puppetsLock.Lock()
	defer bridge.puppetsLock.Unlock()
	puppet, ok := bridge.puppets[jid]
	if !ok {
		bridge.Log.Debugln("GetPuppetByJID(NewPuppet):", jid)
		dbPuppet := bridge.DB.Puppet.Get(jid)
		if dbPuppet == nil {
			dbPuppet = bridge.DB.Puppet.New()
			dbPuppet.JID = jid
			dbPuppet.Insert()
		}
		puppet = bridge.NewPuppet(dbPuppet)
		bridge.puppets[puppet.JID] = puppet
		if len(puppet.CustomMXID) > 0 {
			bridge.puppetsByCustomMXID[puppet.CustomMXID] = puppet
		}
	}
	return puppet
}

func (bridge *Bridge) GetPuppetByCustomMXID(mxid id.UserID) *Puppet {
	bridge.puppetsLock.Lock()
	defer bridge.puppetsLock.Unlock()
	puppet, ok := bridge.puppetsByCustomMXID[mxid]
	if !ok {
		dbPuppet := bridge.DB.Puppet.GetByCustomMXID(mxid)
		if dbPuppet == nil {
			return nil
		}
		bridge.Log.Debugln("GetPuppetByCustomMXID(NewPuppet):", dbPuppet.JID)
		puppet = bridge.NewPuppet(dbPuppet)
		bridge.puppets[puppet.JID] = puppet
		bridge.puppetsByCustomMXID[puppet.CustomMXID] = puppet
	}
	return puppet
}

func (bridge *Bridge) GetAllPuppetsWithCustomMXID() []*Puppet {
	return bridge.dbPuppetsToPuppets(bridge.DB.Puppet.GetAllWithCustomMXID())
}

func (bridge *Bridge) GetAllPuppets() []*Puppet {
	return bridge.dbPuppetsToPuppets(bridge.DB.Puppet.GetAll())
}

func (bridge *Bridge) dbPuppetsToPuppets(dbPuppets []*database.Puppet) []*Puppet {
	bridge.puppetsLock.Lock()
	defer bridge.puppetsLock.Unlock()
	output := make([]*Puppet, len(dbPuppets))
	for index, dbPuppet := range dbPuppets {
		if dbPuppet == nil {
			continue
		}
		puppet, ok := bridge.puppets[dbPuppet.JID]
		if !ok {
			bridge.Log.Debugln("dbPuppetsToPuppets(NewPuppet):", dbPuppet.JID)
			puppet = bridge.NewPuppet(dbPuppet)
			bridge.puppets[dbPuppet.JID] = puppet
			if len(dbPuppet.CustomMXID) > 0 {
				bridge.puppetsByCustomMXID[dbPuppet.CustomMXID] = puppet
			}
		}
		output[index] = puppet
	}
	return output
}

func (bridge *Bridge) NewPuppet(dbPuppet *database.Puppet) *Puppet {
	return &Puppet{
		Puppet: dbPuppet,
		bridge: bridge,
		log:    bridge.Log.Sub(fmt.Sprintf("Puppet/%s", dbPuppet.JID)),

		MXID: id.NewUserID(
			bridge.Config.Bridge.FormatUsername(
				//	dbPuppet.JID,
				//),
				strings.Replace(
					strings.Replace(dbPuppet.JID, whatsappExt.NewUserSuffix, "", 1),
					":",
					"-",
					-1,
					),
				),
			bridge.Config.Homeserver.Domain),
	}
}

type Puppet struct {
	*database.Puppet

	bridge *Bridge
	log    log.Logger

	typingIn id.RoomID
	typingAt int64

	MXID id.UserID

	customIntent   *appservice.IntentAPI
	customTypingIn map[id.RoomID]bool
	customUser     *User
}

func (puppet *Puppet) PhoneNumber() string {
	return strings.Replace(puppet.JID, whatsappExt.NewUserSuffix, "", 1)
}

func (puppet *Puppet) IntentFor(portal *Portal) *appservice.IntentAPI {
	fmt.Println()
	fmt.Printf("puppent IntentFor: %+v", puppet)
	fmt.Println()
	if (!portal.IsPrivateChat() && puppet.customIntent == nil) ||
		(portal.backfilling && portal.bridge.Config.Bridge.InviteOwnPuppetForBackfilling) ||
		portal.Key.JID + skypeExt.NewUserSuffix == puppet.JID {
		fmt.Println()
		fmt.Println("puppent IntentFor0:", portal.Key.JID, puppet.JID)
		fmt.Println("puppent IntentFor0:", portal.Key.JID, puppet.JID)
		fmt.Println()
		return puppet.DefaultIntent()
	}
	fmt.Println()
	fmt.Printf("puppent IntentFor2: %+v", puppet.customIntent)
	fmt.Println()
	if portal.IsPrivateChat() && puppet.customIntent == nil{
		return puppet.DefaultIntent()
	}
	return puppet.customIntent
}

func (puppet *Puppet) CustomIntent() *appservice.IntentAPI {
	return puppet.customIntent
}

func (puppet *Puppet) DefaultIntent() *appservice.IntentAPI {
	fmt.Println()
	fmt.Println("DefaultIntent puppet.MXID: ", puppet.MXID)
	fmt.Println()
	return puppet.bridge.AS.Intent(puppet.MXID)
}

func (puppet *Puppet) UpdateAvatar(source *User, avatar *skypeExt.ProfilePicInfo) bool {
	if avatar == nil {
		return false
		//var err error
		//avatar, err = source.Conn.GetProfilePicThumb(puppet.JID)
		//if err != nil {
		//	puppet.log.Warnln("Failed to get avatar:", err)
		//	return false
		//}
	}

	if avatar.Status != 0 {
		return false
	}

	if avatar.Tag == puppet.Avatar {
		return false
	}

	if len(avatar.URL) == 0 {
		err := puppet.DefaultIntent().SetAvatarURL(id.ContentURI{})
		if err != nil {
			puppet.log.Warnln("Failed to remove avatar:", err)
		}
		puppet.AvatarURL = id.ContentURI{}
		puppet.Avatar = avatar.Tag
		go puppet.updatePortalAvatar()
		return true
	}

	data, err := avatar.DownloadBytes()
	if err != nil {
		puppet.log.Warnln("Failed to download avatar:", err)
		return false
	}

	mime := http.DetectContentType(data)
	resp, err := puppet.DefaultIntent().UploadBytes(data, mime)
	if err != nil {
		puppet.log.Warnln("Failed to upload avatar:", err)
		return false
	}

	puppet.AvatarURL = resp.ContentURI
	err = puppet.DefaultIntent().SetAvatarURL(puppet.AvatarURL)
	if err != nil {
		puppet.log.Warnln("Failed to set avatar:", err)
	}
	puppet.Avatar = avatar.Tag
	go puppet.updatePortalAvatar()
	return true
}

func (puppet *Puppet) UpdateName(source *User, contact skype.Contact) bool {
	newName, quality := puppet.bridge.Config.Bridge.FormatDisplayname(contact)
	if puppet.Displayname != newName && quality >= puppet.NameQuality {
		err := puppet.DefaultIntent().SetDisplayName(newName)
		if err == nil {
			puppet.Displayname = newName
			puppet.NameQuality = quality
			go puppet.updatePortalName()
			puppet.Update()
		} else {
			puppet.log.Warnln("Failed to set display name:", err)
		}
		return true
	}
	return false
}

func (puppet *Puppet) updatePortalMeta(meta func(portal *Portal)) {
	if puppet.bridge.Config.Bridge.PrivateChatPortalMeta {
		for _, portal := range puppet.bridge.GetAllPortalsByJID(puppet.JID) {
			meta(portal)
		}
	}
}

func (puppet *Puppet) updatePortalAvatar() {
	puppet.updatePortalMeta(func(portal *Portal) {
		if len(portal.MXID) > 0 {
			_, err := portal.MainIntent().SetRoomAvatar(portal.MXID, puppet.AvatarURL)
			if err != nil {
				portal.log.Warnln("Failed to set avatar:", err)
			}
		}
		portal.AvatarURL = puppet.AvatarURL
		portal.Avatar = puppet.Avatar
		portal.Update()
	})
}

func (puppet *Puppet) updatePortalName() {
	puppet.updatePortalMeta(func(portal *Portal) {
		if len(portal.MXID) > 0 {
			_, err := portal.MainIntent().SetRoomName(portal.MXID, puppet.Displayname)
			if err != nil {
				portal.log.Warnln("Failed to set name:", err)
			}
		}
		portal.Name = puppet.Displayname
		portal.Update()
	})
}

func (puppet *Puppet) Sync(source *User, contact skype.Contact) {
	fmt.Println("sync")
	err := puppet.DefaultIntent().EnsureRegistered()
	if err != nil {
		puppet.log.Errorln("Failed to ensure registered:", err)
	}

	//if contact.Jid == source.JID {
	//	contact.Notify = source.Conn.Info.Pushname
	//}
	avatar := &skypeExt.ProfilePicInfo{
		URL: contact.Profile.AvatarUrl,
		Tag: contact.Profile.AvatarUrl,
		Status: 0,
	}
	update := false
	update = puppet.UpdateName(source, contact) || update
	update = puppet.UpdateAvatar(source, avatar) || update
	if update {
		puppet.Update()
	}
}
