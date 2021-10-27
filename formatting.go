package main

import (
	"fmt"
	skypeExt "github.com/kelaresg/matrix-skype/skype-ext"
	"html"
	"regexp"
	"strings"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"github.com/kelaresg/matrix-skype/types"
)

var italicRegex = regexp.MustCompile("([\\s>~*]|^)_(.+?)_([^a-zA-Z\\d]|$)")
var boldRegex = regexp.MustCompile("([\\s>_~]|^)\\*(.+?)\\*([^a-zA-Z\\d]|$)")
var strikethroughRegex = regexp.MustCompile("([\\s>_*]|^)~(.+?)~([^a-zA-Z\\d]|$)")
var codeBlockRegex = regexp.MustCompile("```(?:.|\n)+?```")

//var mentionRegex = regexp.MustCompile("@[0-9]+")
//var mentionRegex = regexp.MustCompile("@(.*)")
var mentionRegex = regexp.MustCompile("<at[^>]+\\bid=\"([^\"]+)\"(.*?)</at>*")

type Formatter struct {
	bridge *Bridge

	matrixHTMLParser *format.HTMLParser

	waReplString   map[*regexp.Regexp]string
	waReplFunc     map[*regexp.Regexp]func(string) string
	waReplFuncText map[*regexp.Regexp]func(string) string
}

func NewFormatter(bridge *Bridge) *Formatter {
	formatter := &Formatter{
		bridge: bridge,
		matrixHTMLParser: &format.HTMLParser{
			TabsToSpaces: 4,
			Newline:      "\n",

			PillConverter: func(mxid, eventID string, ctx format.Context) string {
				if mxid[0] == '@' {
					puppet := bridge.GetPuppetByMXID(id.UserID(mxid))
					if puppet != nil {
						return "@" + puppet.PhoneNumber()
					}
				}
				return mxid
			},
			BoldConverter: func(text string, _ format.Context) string {
				return fmt.Sprintf("*%s*", text)
			},
			ItalicConverter: func(text string, _ format.Context) string {
				return fmt.Sprintf("_%s_", text)
			},
			StrikethroughConverter: func(text string, _ format.Context) string {
				return fmt.Sprintf("~%s~", text)
			},
			MonospaceConverter: func(text string, _ format.Context) string {
				return fmt.Sprintf("```%s```", text)
			},
			MonospaceBlockConverter: func(text, language string, _ format.Context) string {
				return fmt.Sprintf("```%s```", text)
			},
		},
		waReplString: map[*regexp.Regexp]string{
			italicRegex:        "$1<em>$2</em>$3",
			boldRegex:          "$1<strong>$2</strong>$3",
			strikethroughRegex: "$1<del>$2</del>$3",
		},
	}
	formatter.waReplFunc = map[*regexp.Regexp]func(string) string{
		codeBlockRegex: func(str string) string {
			str = str[3 : len(str)-3]
			if strings.ContainsRune(str, '\n') {
				return fmt.Sprintf("<pre><code>%s</code></pre>", str)
			}
			return fmt.Sprintf("<code>%s</code>", str)
		},
	}
	formatter.waReplFuncText = map[*regexp.Regexp]func(string) string{
	}
	return formatter
}

func (formatter *Formatter) getMatrixInfoByJID(jid types.SkypeID) (mxid id.UserID, displayname string) {
	if user := formatter.bridge.GetUserByJID(jid); user != nil {
		mxid = user.MXID
		displayname = string(user.MXID)
	} else if puppet := formatter.bridge.GetPuppetByJID(jid); puppet != nil {
		mxid = puppet.MXID
		displayname = puppet.Displayname
	}
	return
}

func (formatter *Formatter) ParseSkype(content *event.MessageEventContent, RoomMXID id.RoomID) {
	// parse '<a><a/>' tag
	reg:= regexp.MustCompile(`(?U)(<a .*>(.*)</a>)`)
	bodyMatch := reg.FindAllStringSubmatch(content.Body, -1)
	for _, match := range bodyMatch {
		content.Body = strings.ReplaceAll(content.Body, match[1], match[2])
	}

	output := content.Body
	for regex, replacement := range formatter.waReplString {
		output = regex.ReplaceAllString(output, replacement)
	}
	for regex, replacer := range formatter.waReplFunc {
		output = regex.ReplaceAllStringFunc(output, replacer)
	}
	content.Body = html.UnescapeString(content.Body)

	var backStr string
	if output != content.Body {
		output = strings.Replace(output, "\n", "<br/>", -1)
		content.FormattedBody = output
		content.Format = event.FormatHTML
		var mxid id.UserID

		// parse quote message(set reply)
		content.Body = strings.ReplaceAll(content.Body, "\n", "")
		quoteReg := regexp.MustCompile(`<quote[^>]+\bauthor="([^"]+)" authorname="([^"]+)" timestamp="([^"]+)" conversation="([^"]+)" messageid="([^"]+)".*>.*?</legacyquote>(.*?)<legacyquote>.*?</legacyquote></quote>(.*)`)
		quoteMatches := quoteReg.FindAllStringSubmatch(content.Body, -1)

		if len(quoteMatches) > 0 {
			for _, match := range quoteMatches {
				for index, a := range match {
					fmt.Println("index: ", index)
					fmt.Println("ParseSkype quoteMatches a:", a)
					fmt.Println()
				}
				portal := formatter.bridge.GetPortalByMXID(RoomMXID)
				if portal.Key.JID != match[4] {
					content.FormattedBody = match[6]
					content.Body = fmt.Sprintf("%s\n\n", match[6])

					// this means that there are forwarding messages across groups
					if strings.HasSuffix(match[4], skypeExt.GroupSuffix) || strings.HasSuffix(portal.Key.JID, skypeExt.GroupSuffix){
						continue
					}
				}
				msgMXID := ""
				msg := formatter.bridge.DB.Message.GetByID(match[5])
				if msg != nil {
					msgMXID = string(msg.MXID)
				}
				mxid, _ = formatter.getMatrixInfoByJID("8:" + match[1] + skypeExt.NewUserSuffix)
				href1 := fmt.Sprintf(`https://%s/#/room/%s/%s?via=%s`, formatter.bridge.Config.Homeserver.ServerName, RoomMXID, msgMXID, formatter.bridge.Config.Homeserver.Domain)
				href2 := fmt.Sprintf(`https://%s/#/user/%s`, formatter.bridge.Config.Homeserver.ServerName, mxid)
				newContent := fmt.Sprintf(`<mx-reply><blockquote><a href="%s">In reply to</a> <a href="%s">%s</a><br>%s</blockquote></mx-reply>`,
					href1,
					href2,
					mxid,
					match[6])
				content.FormattedBody = newContent
				content.Body = fmt.Sprintf("> <%s> %s\n\n", mxid, match[6])
				inRelateTo := &event.RelatesTo{
					Type: event.RelReply,
					EventID: id.EventID(msgMXID),
				}
				content.SetRelatesTo(inRelateTo)
				backStr = match[7]
			}
		}
	}

	// parse mention user message
	r := regexp.MustCompile(`(?m)<at[^>]+\bid="([^"]+)">(.*?)</at>`)
	var originStr string
	var originBodyStr string
	if len(backStr) == 0 {
		originStr = content.Body
	} else {
		originStr = backStr
	}
	matches := r.FindAllStringSubmatch(originStr, -1)
	if len(matches) > 0 {
		for _, match := range matches {
			mxid, displayname := formatter.getMatrixInfoByJID(match[1] + skypeExt.NewUserSuffix)
			replaceStr := ""
			if len(displayname) < 1 {
				// TODO need to optimize
				replaceStr = match[2] + ":"
			} else {
				replaceStr = fmt.Sprintf(`<a href="https://%s/#/%s">%s</a>:`, formatter.bridge.Config.Homeserver.ServerName, mxid, displayname)
			}
			// number := "@" + strings.Replace(match[1], skypeExt.NewUserSuffix, "", 1)
			originStr = strings.ReplaceAll(originStr, match[0], replaceStr)
			originBodyStr = strings.ReplaceAll(originStr, replaceStr, displayname + ":")
		}
		if len(backStr) == 0 {
			content.Format = event.FormatHTML
			content.Body = originBodyStr
			content.FormattedBody = originStr
		} else {
			content.Body = content.Body + originBodyStr
			content.FormattedBody = content.FormattedBody + originStr
		}
	} else {
		content.Body = content.Body + backStr
		content.FormattedBody = content.FormattedBody + backStr
	}

	//filter edit tag
	e := regexp.MustCompile(`(<e_m a=".*></e_m>)`)
	editMatches := e.FindAllStringSubmatch(content.Body, -1)
	if len(editMatches) > 0 {
		for _, match := range editMatches {
			content.Body = strings.ReplaceAll(content.Body, match[0], "")
			content.FormattedBody = strings.ReplaceAll(content.FormattedBody, match[0], "")
		}
	}
}

func (formatter *Formatter) ParseMatrix(html string) string {
	ctx := make(format.Context)
	return formatter.matrixHTMLParser.Parse(html, ctx)
}
