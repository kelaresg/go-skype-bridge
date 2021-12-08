module github.com/kelaresg/matrix-skype

go 1.14

require (
	github.com/gabriel-vasile/mimetype v1.1.2
	github.com/gorilla/websocket v1.4.2
	github.com/kelaresg/go-skypeapi v0.1.2-0.20210813144457-5bc29092a74e
	github.com/lib/pq v1.9.0
	github.com/mattn/go-sqlite3 v2.0.3+incompatible
	github.com/pkg/errors v0.9.1
	gopkg.in/yaml.v2 v2.4.0
	maunium.net/go/mauflag v1.0.0
	maunium.net/go/maulogger/v2 v2.3.1
	maunium.net/go/mautrix v0.10.3
)

replace maunium.net/go/mautrix => github.com/pidongqianqian/mautrix-go v0.10.4-0.20211208080648-321c4f849adb
