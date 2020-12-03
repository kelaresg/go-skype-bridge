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

package main

import (
	skype "github.com/kelaresg/go-skypeapi"
	"strings"
	"time"
)

func (handler *CommandHandler) CommandSpecialMux(ce *CommandEvent) {
	switch ce.Command {
	case "special-create":
		if !ce.User.HasSession() {
			ce.Reply("You are not logged in. Use the `login` command to log into Skype.")
			return
		}
		switch ce.Command {
		case "special-create":
			handler.CommandSpecialCreate(ce)
		}
	default:
		ce.Reply("Unknown Command")
	}
}

func (handler *CommandHandler) CommandSpecialHelp(ce *CommandEvent) {
	cmdPrefix := ""
	if ce.User.ManagementRoom != ce.RoomID || ce.User.IsRelaybot {
		cmdPrefix = handler.bridge.Config.Bridge.CommandPrefix + " "
	}

	ce.Reply("* " + strings.Join([]string{
		cmdPrefix + cmdSpecialCreateHelp,
	}, "\n* "))
}

const cmdSpecialCreateHelp = `special-create <_topic_> <_member user id_>,... - Create a group.`

func (handler *CommandHandler) CommandSpecialCreate(ce *CommandEvent) {
	if len(ce.Args) < 2 {
		ce.Reply("**Usage:** `special-create <topic> <member user id>,...`")
		return
	}

	user := ce.User
	topic := ce.Args[0]
	members := skype.Members{}

	// The user who created the group must be in the members and have "Admin" rights
	userId := ce.User.Conn.UserProfile.Username
	member2 := skype.Member{
		Id:   "8:" + userId,
		Role: "Admin",
	}

	members.Members = append(members.Members, member2)
	members.Properties = skype.Properties{
		HistoryDisclosed: "true",
		Topic:            topic,
	}

	handler.log.Debugln("Create Group", topic, "with", members)
	err := user.Conn.HandleGroupCreate(members)
	inputArr := strings.Split(ce.Args[1], ",")
	members = skype.Members{}
	for _, memberId := range inputArr {
		members.Members = append(members.Members, skype.Member{
			Id:   memberId,
			Role: "Admin",
		})
	}
	conversationId, ok := <-user.Conn.CreateChan
	if ok {
		err = user.Conn.AddMember(members, conversationId)
	}
	if err != nil {
		ce.Reply("Please confirm that parameters is correct.")
	} else {
		ce.Reply("Syncing group list...")
		time.Sleep(time.Duration(3) * time.Second)
		ce.Reply("Syncing group list completed")
	}
}

