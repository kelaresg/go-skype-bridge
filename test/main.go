package main

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"time"
)

func find(htm string, re *regexp.Regexp) [][]string {
	imgs := re.FindAllStringSubmatch(htm, -1)
	//fmt.Println(re.FindAllStringIndex(htm, -1))
	//return imgs
	//out := make([]string, len(imgs))
	for _, img := range imgs {
		for _, img2 := range img {
		fmt.Println(img2)
		}
	}
	return imgs
}
func main () {
	a := "2020-07-15T03:47:51.217Z"
	t, _ := time.Parse(time.RFC3339, a)
	fmt.Println(t.Unix())
	return
	//str := `1<at id="8:live:zhaosl_4">lyle</at>21211<at id="8:live:zhaosl_4">lyle</at>2121`
	// str := `<mx-reply><blockquote><a href="https://matrix.to/#/!kpouCkfhzvXgbIJmkP:oliver.matrix.host/$5lsAX5KU6YOFlKF83EM7ngRn82b1-FfpwAIK_tG2PiQ?via=oliver.matrix.host">In reply to</a> <a href="https://matrix.to/#/@skype&8-live-1163765691:oliver.matrix.host">@skype&8-live-1163765691:oliver.matrix.host</a><br>qqqqqqq</blockquote></mx-reply>9999999`
	//str := `<quote author="live:1163765691" authorname="Oliver1 Zhao2↵" timestamp="1594719165" conversation="19:2a13d0f6ae144a6282e4b35aefdb6444@thread.skype" messageid="1594719164753" cuid="15947191637000135303"><legacyquote>[1594719165] Oliver1 Zhao2↵: </legacyquote>3333333<legacyquote>↵↵&lt;&lt;&lt; </legacyquote></quote>1111111`
	str := `<quote author="live:1163765691" authorname="Oliver1 Zhao2
" timestamp="1594808528" conversation="19:2a13d0f6ae144a6282e4b35aefdb6444@thread.skype" messageid="1594808528203" cuid="14982010260376431987"><legacyquote>[1594808528] Oliver1 Zhao2
: </legacyquote>00000000<legacyquote>
&lt;&lt;&lt; </legacyquote></quote>1111111111`
	//r,_:=regexp.Compile(".<at id=\"(.*)\"></at>*")
	//r := regexp.MustCompile(`<at[^>]+\bid="([^"]+)"(.*?)</at>*`)
	//r := regexp.MustCompile(`<a[^>]+\bhref="(.*?)://matrix\.to/#/@skype&amp;([^"]+):(.*?)">(.*?)</a>*`)
	str = strings.ReplaceAll(str, "\n", "")
	r := regexp.MustCompile(`<quote[^>]+\bauthor="([^"]+)" authorname="([^"]+)" timestamp="([^"]+)".*>.*?</legacyquote>(.*?)<legacyquote>.*?</legacyquote></quote>(.*)`)
	//patten := `<at id="(a-z)"></at>`
	find(str, r)
	//fmt.Println(find(str, r))
}
	//cli, _ := skype.NewConn()
	//_ = cli.Login("1", "3")
	//avatar := &skypeExt.ProfilePicInfo{
	//	URL:    "https://api.asm.skype.com/v1/objects/0-ea-d6-ee876d1872e567ed85d89efae9b05971/views/swx_avatar",
	//	Tag:    "https://api.asm.skype.com/v1/objects/0-ea-d6-ee876d1872e567ed85d89efae9b05971/views/swx_avatar",
	//	Status: 0,
	//	Authorization: "skype_token " + cli.LoginInfo.SkypeToken,
	//}

	//data, _ := avatar.DownloadBytes()
	//fmt.Println("DownloadBytes: ", string(data))
	//type user struct {
	//	Presences map[string]*skypeExt.Presence
	//}
	//a := user{Presences:make(map[string]*skypeExt.Presence)}
	//a.Presences["1"] = &skypeExt.Presence{
	//	Id: "1",
	//	Availability: "313",
	//	Status: "31312",
	//}
	//fmt.Printf("%+v", a)
	//body := `{"eventMessages":[{"id":1005,"type":"EventMessage","resourceType":"NewMessage","time":"2020-06-19T11:19:57Z","resourceLink":"https://azwcus1-client-s.gateway.messenger.live.com/v1/users/ME/conversations/19:77d9cf34f8d6419fbb3542bd6304ac33@thread.skype/messages/1592565597315","resource":{"contentformat":"FN=MS%20Shell%20Dlg; EF=; CO=0; CS=0; PF=0","messagetype":"ThreadActivity/PictureUpdate","originalarrivaltime":"2020-06-19T11:19:57.315Z","ackrequired":"https://azwcus1-client-s.gateway.messenger.live.com/v1/users/ME/conversations/ALL/messages/1592565597315/ack","type":"Message","version":"1592565597315","contenttype":"text/plain; charset=UTF-8","origincontextid":"8571783950420663070","isactive":false,"from":"https://azwcus1-client-s.gateway.messenger.live.com/v1/users/ME/contacts/19:77d9cf34f8d6419fbb3542bd6304ac33@thread.skype","id":"1592565597315","conversationLink":"https://azwcus1-client-s.gateway.messenger.live.com/v1/users/ME/conversations/19:77d9cf34f8d6419fbb3542bd6304ac33@thread.skype","counterpartymessageid":"1592565597315","threadtopic":"gteat4","content":"<pictureupdate><eventtime>1592565597440</eventtime><initiator>8:live:.cid.d3feb90dceeb51cc</initiator><value>URL@https://api.asm.skype.com/v1/objects/0-ea-d1-df4643685906b8826aaf6faddbbd572d/views/avatar_fullsize</value></pictureupdate>","composetime":"2020-06-19T11:19:57.315Z"}}]}`
	//var bodyContent struct {
	//	EventMessages []skype.Conversation `json:"eventMessages"`
	//}
	//_ = json.Unmarshal([]byte(body), &bodyContent)
	//if len(bodyContent.EventMessages) > 0 {
	//	for _, message := range bodyContent.EventMessages {
	//		if message.Type == "EventMessage" {
	//			messageType := skypeExt.ChatActionType(message.Resource.MessageType)
	//			switch messageType {
	//			case skypeExt.TopicUpdate:
	//				topicContent := skype.TopicContent{}
	//				//把xml数据解析成bs对象
	//				xml.Unmarshal([]byte(message.Resource.Content), &topicContent)
	//				message.Resource.SendId = topicContent.Initiator + skypeExt.NewUserSuffix
	//				//go portal.UpdateName(cmd.ThreadTopic, cmd.SendId)
	//			case skypeExt.PictureUpdate:
	//				topicContent := skype.PictureContent{}
	//				//把xml数据解析成bs对象
	//				xml.Unmarshal([]byte(message.Resource.Content), &topicContent)
	//				message.Resource.SendId = topicContent.Initiator + skypeExt.NewUserSuffix
	//				url := strings.TrimPrefix(topicContent.Value, "URL@")
	//				fmt.Println(url)
	//				//avatar := &skypeExt.ProfilePicInfo{
	//				//	URL:    url,
	//				//	Tag:    topicContent.Value,
	//				//	Status: 0,
	//				//}
	//				//go portal.UpdateAvatar(user, avatar)
	//			}
	//		}
	//	}
	//}

	//fmt.Println("hello https://tool.lu/")
	//membersStr := "[\"8:live:1163765691\",\"8:live:zhaosl_4\"]"
	//var members []string
	//_ = json.Unmarshal([]byte(membersStr), &members)
	//fmt.Println(members)
	//for _, participant := range members {
	//	fmt.Println(participant)
	//}
	//type a string
	//type b struct {
	//	C a
	//}
	//v := b{
	//	C: a("8:live:1163765691"),
	//}
	//fmt.Println(strings.HasSuffix("28:0d5d6cff-595d-49d7-9cf8-973173f5233b@s.skype.net", skypeExt.NewUserSuffix))
	//return
	//rand.Seed(time.Now().UnixNano())
	//fmt.Sprintf("%04v", rand.New(rand.NewSource(time.Now().UnixNano())).Intn(10000))
	//currentTimeNanoStr := strconv.FormatInt(time.Now().UnixNano(), 10)
	//currentTimeNanoStr = currentTimeNanoStr[:len(currentTimeNanoStr)-3]
	////clientmessageid := currentTimeNanoStr + randomStr
	//fmt.Println(fmt.Sprintf("%04v", rand.New(rand.NewSource(time.Now().UnixNano())).Intn(10000)))
	//return
	////Parse("@whatsapp_8:live:1163765691:oliver.matrix.host")
	//userIDRegex, _ := regexp.Compile("@whatsapp_8:live:1163765691:oliver.matrix.host")
	//match := userIDRegex.FindStringSubmatch(string("@whatsapp_8:live:1163765691:oliver.matrix.host"))
	//fmt.Println(match)
//}

func Parse(userID string)(localpart, homeserver string, err error) {
	if len(userID) == 0 || userID[0] != '@' || !strings.ContainsRune(string(userID), ':') {
		err = fmt.Errorf("%s is not a valid user id", userID)
		return
	}
	parts := strings.Split(string(userID), ":")
	localpart, homeserver = strings.TrimPrefix(strings.Join(parts[:len(parts)-1], ":"), "@"), parts[len(parts)-1]
	localpart = base64.StdEncoding.EncodeToString([]byte(localpart))
	fmt.Println(localpart, homeserver)
	return
}
