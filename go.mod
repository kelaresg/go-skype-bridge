module github.com/kelaresg/matrix-skype

go 1.14

require (
	github.com/chai2010/webp v1.1.0
	github.com/gabriel-vasile/mimetype v1.1.2
	github.com/gorilla/websocket v1.4.2
	github.com/kelaresg/go-skypeapi v0.1.2-0.20210526124154-2e6d23e27010
	github.com/lib/pq v1.9.0
	github.com/mattn/go-sqlite3 v2.0.3+incompatible
	github.com/pkg/errors v0.9.1
	golang.org/x/image v0.0.0-20200618115811-c13761719519
	gopkg.in/yaml.v2 v2.3.0
	maunium.net/go/mauflag v1.0.0
	maunium.net/go/maulogger/v2 v2.2.4
	maunium.net/go/mautrix v0.8.0-rc.4
)

replace maunium.net/go/mautrix => github.com/pidongqianqian/mautrix-go v0.9.11-0.20210508035357-93e21d8c2bbe
