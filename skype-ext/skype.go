// matrix-skype - A Matrix-WhatsApp puppeting bridge.
// Copyright (C) 2019 Tulir Asokan
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package skypeExt

import (
	"encoding/json"
	"fmt"
	"github.com/kelaresg/go-skypeapi"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

const (
	OldUserSuffix = ""
	NewUserSuffix = "@s.skype.net"
	GroupSuffix = "@thread.skype"
)

type ExtendedConn struct {
	*skype.Conn

	handlers []skype.Handler
}

func ExtendConn(conn *skype.Conn) *ExtendedConn {
	ext := &ExtendedConn{
		Conn: conn,
	}
	ext.Conn.AddHandler(ext)
	return ext
}

func (ext *ExtendedConn) AddHandler(handler skype.Handler) {
	ext.Conn.AddHandler(handler)
	ext.handlers = append(ext.handlers, handler)
}


func (ext *ExtendedConn) RemoveHandler(handler skype.Handler) bool {
	ext.Conn.RemoveHandler(handler)
	for i, v := range ext.handlers {
		if v == handler {
			ext.handlers = append(ext.handlers[:i], ext.handlers[i+1:]...)
			return true
		}
	}
	return false
}

func (ext *ExtendedConn) RemoveHandlers() {
	ext.Conn.RemoveHandlers()
	ext.handlers = make([]skype.Handler, 0)
}

func (ext *ExtendedConn) shouldCallSynchronously(handler skype.Handler) bool {
	//sh, ok := handler.(skype.SyncHandler)
	//return ok && sh.ShouldCallSynchronously()
	return true
}

func (ext *ExtendedConn) ShouldCallSynchronously() bool {
	return true
}

type GroupInfo struct {
	JID      string `json:"jid"`
	OwnerJID string `json:"owner"`

	Name        string `json:"subject"`
	NameSetTime int64  `json:"subjectTime"`
	NameSetBy   string `json:"subjectOwner"`

	Topic      string `json:"desc"`
	TopicID    string `json:"descId"`
	TopicSetAt int64  `json:"descTime"`
	TopicSetBy string `json:"descOwner"`

	GroupCreated int64 `json:"creation"`

	Status int16 `json:"status"`

	Participants []struct {
		JID          string `json:"id"`
		IsAdmin      bool   `json:"isAdmin"`
		IsSuperAdmin bool   `json:"isSuperAdmin"`
	} `json:"participants"`
}

func (ext *ExtendedConn) GetGroupMetaData(jid string) (*GroupInfo, error) {
	data, err := ext.Conn.GetConversation(jid)
	if err != nil {
		return nil, fmt.Errorf("failed to get group metadata: %v", err)
	}
	//content := <-data
	//info := data
	membersStr := data.ThreadProperties.Members
	var members []string
	if len(membersStr) < 1 {
		resp, _ := ext.Conn.GetConsumptionHorizons(jid)
		for _, con := range resp.ConsumptionHorizons {
			members = append(members, con.Id)
		}
	} else {
		err = json.Unmarshal([]byte(membersStr), &members)
	}
	info := &GroupInfo{}
	fmt.Println()
	fmt.Println("GetGroupMetaData: ", members)
	fmt.Println()
	for _, participant := range members {
		type a  struct {
			JID          string `json:"id"`
			IsAdmin      bool   `json:"isAdmin"`
			IsSuperAdmin bool   `json:"isSuperAdmin"`
		}
		isSuperAdmin := false
		if "8:" + ext.Conn.UserProfile.Username == participant {
			isSuperAdmin = true
		}
		info.Participants = append(info.Participants, a{
			participant + NewUserSuffix,
			false,
			isSuperAdmin,
		})

		personId := participant + NewUserSuffix
		if _, ok := ext.Conn.Store.Contacts[personId]; !ok {
			participantArr := strings.Split(participant, "8:")
			if participantArr[1] != "" {
				ext.Conn.NameSearch(participantArr[1])
			}
		}
	}
	info.Topic = data.ThreadProperties.Topic
	info.Name = data.ThreadProperties.Topic
	fmt.Println()
	fmt.Println("GetGroupMetaData:3 ")
	fmt.Println()
	//info.NameSetBy = info.NameSetBy + NewUserSuffix
	//info.TopicSetBy = info.TopicSetBy + NewUserSuffix

	return info, nil
}

type ProfilePicInfo struct {
	URL string `json:"eurl"`
	Tag string `json:"tag"`

	Status int16 `json:"status"`
	Authorization string `json:"authorization"`
}

func (ppi *ProfilePicInfo) Download() (io.ReadCloser, error) {
	if ppi.Authorization != "" {
		client := &http.Client{
			Timeout: 20 * time.Second,
		}
		headers := map[string]string{
			"X-Client-Version": "0/0.0.0.0",
			"Authorization":    ppi.Authorization, // "skype_token " + Conn.LoginInfo.SkypeToken,
		}
		req, err := http.NewRequest("GET", ppi.URL, nil)
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		return resp.Body, nil
	}else {
		resp, err := http.Get(ppi.URL)
		if err != nil {
			return nil, err
		}
		return resp.Body, nil
	}

}

func (ppi *ProfilePicInfo) DownloadBytes() ([]byte, error) {
	body, err := ppi.Download()
	if err != nil {
		return nil, err
	}
	defer body.Close()
	data, err := ioutil.ReadAll(body)
	return data, err
}

func (ext *ExtendedConn) GetProfilePicThumb(jid string) (*ProfilePicInfo, error) {
	//data, err := ext.Conn.GetProfilePicThumb(jid)
	//if err != nil {
	//	return nil, fmt.Errorf("failed to get avatar: %v", err)
	//}
	//content := <-data
	//info := &ProfilePicInfo{}
	//err = json.Unmarshal([]byte(content), info)
	//if err != nil {
	//	return info, fmt.Errorf("failed to unmarshal avatar info: %v", err)
	//}
	//return info, nil
	return nil, nil
}

func (ext *ExtendedConn) HandleGroupInvite(groupJid string, numbers[]string) (err error) {
	var parts []string
	parts = append(parts, numbers...)
	members := skype.Members{}
	members = skype.Members{}
	for _, memberId := range numbers {
		members.Members = append(members.Members, skype.Member{
			Id: "8:"+memberId,
			Role: "Admin",
		})
	}
	err = ext.Conn.AddMember(members, groupJid)
	if err != nil {
		fmt.Printf("%s Handle Invite err", err)
	}
	return
}

func (ext *ExtendedConn) HandleGroupJoin(code string) (err error, codeinfo skype.JoinToConInfo)  {
	err, codeinfo =  ext.Conn.JoinConByCode(code)
	member := skype.Member{
		Id: "8:"+ext.UserProfile.Username,
		Role: "Admin",
	}
	Members := skype.Members{}
	Members.Members = append(Members.Members, member)
	//cli.AddMember(cli.LoginInfo.LocationHost, cli.LoginInfo.SkypeToken, cli.LoginInfo.RegistrationTokenStr, Members, rsp.Resource)
	err = ext.AddMember(Members, codeinfo.Resource)

	member2 := skype.Member{
		Id: "8:"+ext.UserProfile.Username,
		Role: "User",
	}
	mewMembers := skype.Members{}
	mewMembers.Members = append(mewMembers.Members, member2)
	err = ext.AddMember( mewMembers, codeinfo.Resource)
	return
}

func (ext *ExtendedConn) HandleGroupShare(groupJid string) (err error, link string)  {

	res, err := ext.Conn.GetConJoinUrl(groupJid)
	if err != nil {
		return err, ""
	}
	link = res.Url
	return
}


func (ext *ExtendedConn) HandleGroupKick(groupJid string, numbers[]string) (err error) {
	for _, number := range numbers{
		if err == nil {
			err = ext.Conn.RemoveMember(groupJid, number)
		} else {
			_ = ext.Conn.RemoveMember(groupJid, number)
		}
	}
	return
}

func (ext *ExtendedConn) HandleGroupCreate(numbers skype.Members) (err error)  {
	//var parts []string
	//parts = append(parts, numbers...)
	err = ext.Conn.CreateConversationGroup(numbers)
	//if err != nil {
	//	fmt.Printf("%s HandleGroupCreate err", err)
	//}
	return
}

func (ext *ExtendedConn) HandleGroupLeave(groupJid string) (err error)  {
	fmt.Println("groyp id", groupJid)
	fmt.Println("ext.UserProfile.Username", ext.UserProfile.Username)
	err = ext.Conn.RemoveMember(groupJid, "8:"+ext.UserProfile.Username)
	if err != nil {
		fmt.Printf("%s HandleGroupLeave err", err)
	}
	return
}
