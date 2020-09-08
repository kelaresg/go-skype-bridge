package database

import (
	"database/sql"

	log "maunium.net/go/maulogger/v2"

	"github.com/kelaresg/matrix-skype/types"
	"maunium.net/go/mautrix/id"
)

type PuppetQuery struct {
	db  *Database
	log log.Logger
}

func (pq *PuppetQuery) New() *Puppet {
	return &Puppet{
		db:  pq.db,
		log: pq.log,
	}
}

func (pq *PuppetQuery) GetAll() (puppets []*Puppet) {
	rows, err := pq.db.Query("SELECT jid, avatar, avatar_url, displayname, name_quality, custom_mxid, access_token, next_batch FROM puppet")
	if err != nil || rows == nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		puppets = append(puppets, pq.New().Scan(rows))
	}
	return
}

func (pq *PuppetQuery) Get(jid types.SkypeID) *Puppet {
	row := pq.db.QueryRow("SELECT jid, avatar, avatar_url, displayname, name_quality, custom_mxid, access_token, next_batch FROM puppet WHERE jid=$1", jid)
	if row == nil {
		return nil
	}
	return pq.New().Scan(row)
}

func (pq *PuppetQuery) GetByCustomMXID(mxid id.UserID) *Puppet {
	row := pq.db.QueryRow("SELECT jid, avatar, avatar_url, displayname, name_quality, custom_mxid, access_token, next_batch FROM puppet WHERE custom_mxid=$1", mxid)
	if row == nil {
		return nil
	}
	return pq.New().Scan(row)
}

func (pq *PuppetQuery) GetAllWithCustomMXID() (puppets []*Puppet) {
	rows, err := pq.db.Query("SELECT jid, avatar, avatar_url, displayname, name_quality, custom_mxid, access_token, next_batch FROM puppet WHERE custom_mxid<>''")
	if err != nil || rows == nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		puppets = append(puppets, pq.New().Scan(rows))
	}
	return
}

type Puppet struct {
	db  *Database
	log log.Logger

	JID         types.SkypeID
	Avatar      string
	AvatarURL   id.ContentURI
	Displayname string
	NameQuality int8

	CustomMXID  id.UserID
	AccessToken string
	NextBatch   string
}

func (puppet *Puppet) Scan(row Scannable) *Puppet {
	var displayname, avatar, avatarURL, customMXID, accessToken, nextBatch sql.NullString
	var quality sql.NullInt64
	err := row.Scan(&puppet.JID, &avatar, &avatarURL, &displayname, &quality, &customMXID, &accessToken, &nextBatch)
	if err != nil {
		if err != sql.ErrNoRows {
			puppet.log.Errorln("Database scan failed:", err)
		}
		return nil
	}
	puppet.Displayname = displayname.String
	puppet.Avatar = avatar.String
	puppet.AvatarURL, _ = id.ParseContentURI(avatarURL.String)
	puppet.NameQuality = int8(quality.Int64)
	puppet.CustomMXID = id.UserID(customMXID.String)
	puppet.AccessToken = accessToken.String
	puppet.NextBatch = nextBatch.String
	return puppet
}

func (puppet *Puppet) Insert() {
	_, err := puppet.db.Exec("INSERT INTO puppet (jid, avatar, avatar_url, displayname, name_quality, custom_mxid, access_token, next_batch) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
		puppet.JID, puppet.Avatar, puppet.AvatarURL.String(), puppet.Displayname, puppet.NameQuality, puppet.CustomMXID, puppet.AccessToken, puppet.NextBatch)
	if err != nil {
		puppet.log.Warnfln("Failed to insert %s: %v", puppet.JID, err)
	}
}

func (puppet *Puppet) Update() {
	_, err := puppet.db.Exec("UPDATE puppet SET displayname=$1, name_quality=$2, avatar=$3, avatar_url=$4, custom_mxid=$5, access_token=$6, next_batch=$7 WHERE jid=$8",
		puppet.Displayname, puppet.NameQuality, puppet.Avatar, puppet.AvatarURL.String(), puppet.CustomMXID, puppet.AccessToken, puppet.NextBatch, puppet.JID)
	if err != nil {
		puppet.log.Warnfln("Failed to update %s->%s: %v", puppet.JID, err)
	}
}
