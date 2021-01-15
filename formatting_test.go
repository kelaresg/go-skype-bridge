package main

import (
	"github.com/kelaresg/matrix-skype/database"
	"github.com/kelaresg/matrix-skype/types"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"reflect"
	"regexp"
	"sync"
	"testing"
)

func TestFormatter_ParseSkype(t *testing.T) {
	type fields struct {
		bridge           *Bridge
		matrixHTMLParser *format.HTMLParser
		waReplString     map[*regexp.Regexp]string
		waReplFunc       map[*regexp.Regexp]func(string) string
		waReplFuncText   map[*regexp.Regexp]func(string) string
	}
	type args struct {
		content *event.MessageEventContent
	}
	type expect struct {
		content *event.MessageEventContent
	}
	testUser := &User{
		User: &database.User{
			MXID: "mxtestid",
		},
	}
	testBridge := &Bridge{
		usersLock:  *new(sync.Mutex),
		usersByJID: map[types.SkypeID]*User{"test": testUser},
	}
	testFormatter := &Formatter{
		bridge: testBridge,
	}
	tests := []struct {
		name   string
		args   args
		expect expect
	}{
		{
			"simple message",
			args{
				&event.MessageEventContent{
					Body: "This is a very simple message.",
				},
			},
			expect{
				&event.MessageEventContent{
					Body: "This is a very simple message.",
				},
			},
		},
		{
			"simple punctuation test",
			args{
				&event.MessageEventContent{
					Body: "It&apos;s the inclusion of &quot;simple&quot; punctuation that causes most of the problems.",
				},
			},
			expect{
				&event.MessageEventContent{
					Body:   "It's the inclusion of \"simple\" punctuation that causes most of the problems.",
					Format: event.FormatHTML,
				},
			},
		},
		{
			"full punctuation test",
			args{
				&event.MessageEventContent{
					Body:   "&amp;&lt;&gt;&quot;&#39", // use a few different encodings
					Format: event.FormatHTML,
				},
			},
			expect{
				&event.MessageEventContent{
					Body:   "&<>\"'",
					Format: event.FormatHTML,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testFormatter.ParseSkype(tt.args.content, "")
			if !reflect.DeepEqual(tt.args.content, tt.expect.content) {
				t.Errorf("content = %v, wanted %v", tt.args.content, tt.expect.content)
			}
		})
	}
}
