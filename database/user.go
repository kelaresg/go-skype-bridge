package database

import (
	"database/sql"
	"fmt"
	skype "github.com/kelaresg/go-skypeapi"
	skypeExt "github.com/kelaresg/matrix-skype/skype-ext"
	"strings"
	"time"

	log "maunium.net/go/maulogger/v2"

	"github.com/kelaresg/matrix-skype/types"
	"maunium.net/go/mautrix/id"
)

type UserQuery struct {
	db  *Database
	log log.Logger
}

func (uq *UserQuery) New() *User {
	return &User{
		db:  uq.db,
		log: uq.log,
	}
}

func (uq *UserQuery) GetAll() (users []*User) {
	rows, err := uq.db.Query(`SELECT mxid, jid, management_room, last_connection, endpoint_id, skype_token, registration_token, registration_token_str, location_host FROM "user"`)
	if err != nil || rows == nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		users = append(users, uq.New().Scan(rows))
	}
	return
}

func (uq *UserQuery) GetByMXID(userID id.UserID) *User {
	row := uq.db.QueryRow(`SELECT mxid, jid, management_room, last_connection, endpoint_id, skype_token, registration_token, registration_token_str, location_host FROM "user" WHERE mxid=$1`, userID)
	if row == nil {
		return nil
	}
	return uq.New().Scan(row)
}

func (uq *UserQuery) GetByJID(userID types.SkypeID) *User {
	row := uq.db.QueryRow(`SELECT mxid, jid, management_room, last_connection, endpoint_id, skype_token, registration_token, registration_token_str, location_host FROM "user" WHERE jid=$1`, stripSuffix(userID))
	if row == nil {
		return nil
	}
	return uq.New().Scan(row)
}

func (uq *UserQuery) GetPassByMXID(userID id.UserID, password *string) bool {
	row := uq.db.QueryRow(`SELECT password FROM "user" WHERE mxid=$1`, userID)
	if row == nil {
		return false
	}
	err := row.Scan(password)
	return err == nil
}

func (uq *UserQuery) SetPassByMXID(password string, userID id.UserID) bool {
	row := uq.db.QueryRow(`UPDATE "user" SET password=$1 WHERE mxid=$2`, password, userID)
	return row != nil
}

type User struct {
	db  *Database
	log log.Logger

	MXID           id.UserID
	JID            types.SkypeID
	ManagementRoom id.RoomID
	Session        *skype.Session
	LastConnection uint64
}

func (user *User) Scan(row Scannable) *User {
	var jid, endpointId, skypeToken, registrationToken, registrationTokenStr, locationHost sql.NullString
	err := row.Scan(&user.MXID, &jid, &user.ManagementRoom, &user.LastConnection, &endpointId, &skypeToken, &registrationToken, &registrationTokenStr, &locationHost)
	if err != nil {
		if err != sql.ErrNoRows {
			user.log.Errorln("Database scan failed:", err)
		}
		return nil
	}
	if len(jid.String) > 0 && len(endpointId.String) > 0 {
		user.JID = jid.String + skypeExt.NewUserSuffix
		user.Session = &skype.Session{
			EndpointId:           endpointId.String,
			SkypeToken:           skypeToken.String,
			RegistrationToken:    registrationToken.String,
			RegistrationTokenStr: registrationTokenStr.String,
			LocationHost:         locationHost.String,
		}
	} else {
		user.Session = nil
	}
	return user
}

func stripSuffix(jid types.SkypeID) string {
	if len(jid) == 0 {
		return jid
	}

	index := strings.IndexRune(jid, '@')
	if index < 0 {
		return jid
	}

	return jid[:index]
}

func (user *User) jidPtr() *string {
	if len(user.JID) > 0 {
		str := stripSuffix(user.JID)
		return &str
	}
	return nil
}

func (user *User) sessionUnptr() (sess skype.Session) {
	if user.Session != nil {
		sess = *user.Session
	}
	return
}

func (user *User) Insert() {
	sess := user.sessionUnptr()
	_, err := user.db.Exec(`INSERT INTO "user" (mxid, jid, management_room, last_connection, endpoint_id, skype_token, registration_token, registration_token_str, location_host) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		user.MXID, user.jidPtr(),
		user.ManagementRoom, user.LastConnection,
		sess.EndpointId, sess.SkypeToken, sess.RegistrationToken, sess.RegistrationTokenStr, sess.LocationHost)
	if err != nil {
		user.log.Warnfln("Failed to insert %s: %v", user.MXID, err)
	}
}

func (user *User) UpdateLastConnection() {
	user.LastConnection = uint64(time.Now().Unix())
	_, err := user.db.Exec(`UPDATE "user" SET last_connection=$1 WHERE mxid=$2`,
		user.LastConnection, user.MXID)
	if err != nil {
		user.log.Warnfln("Failed to update last connection ts: %v", err)
	}
}

func (user *User) Update() {
	sess := user.sessionUnptr()
	_, err := user.db.Exec(`UPDATE "user" SET jid=$1, management_room=$2, last_connection=$3, endpoint_id=$4, skype_token=$5, registration_token=$6, registration_token_str=$7, location_host=$8 WHERE mxid=$9`,
		user.jidPtr(), user.ManagementRoom, user.LastConnection,
		sess.EndpointId, sess.SkypeToken, sess.RegistrationToken, sess.RegistrationTokenStr, sess.LocationHost,
		user.MXID)
	if err != nil {
		user.log.Warnfln("Failed to update %s: %v", user.MXID, err)
	}
}

type PortalKeyWithMeta struct {
	PortalKey
	InCommunity bool
}

func (user *User) SetPortalKeys(newKeys []PortalKeyWithMeta) error {
	tx, err := user.db.Begin()
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM user_portal WHERE user_jid=$1", user.jidPtr())
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	valueStrings := make([]string, len(newKeys))
	values := make([]interface{}, len(newKeys)*4)
	for i, key := range newKeys {
		pos := i * 4
		valueStrings[i] = fmt.Sprintf("($%d, $%d, $%d, $%d)", pos+1, pos+2, pos+3, pos+4)
		values[pos] = user.jidPtr()
		values[pos+1] = key.JID
		values[pos+2] = key.Receiver
		values[pos+3] = key.InCommunity
	}
	query := fmt.Sprintf("INSERT INTO user_portal (user_jid, portal_jid, portal_receiver, in_community) VALUES %s",
		strings.Join(valueStrings, ", "))
	_, err = tx.Exec(query, values...)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (user *User) CreateUserPortal(newKey PortalKeyWithMeta) {
	user.log.Debugfln("Creating new portal %s for %s", newKey.PortalKey.JID, newKey.PortalKey.Receiver)
	_, err := user.db.Exec(`INSERT INTO user_portal (user_jid, portal_jid, portal_receiver, in_community) VALUES ($1, $2, $3, $4)`,
		user.jidPtr(),
		newKey.PortalKey.JID, newKey.PortalKey.Receiver,
		newKey.InCommunity)
	if err != nil {
		user.log.Warnfln("Failed to insert %s: %v", user.MXID, err)
	}
}

func (user *User) IsInPortal(key PortalKey) bool {
	row := user.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM user_portal WHERE user_jid=$1 AND portal_jid=$2 AND portal_receiver=$3)`, user.jidPtr(), &key.JID, &key.Receiver)
	var exists bool
	_ = row.Scan(&exists)
	return exists
}

func (user *User) GetPortalKeys() []PortalKey {
	rows, err := user.db.Query(`SELECT portal_jid, portal_receiver FROM user_portal WHERE user_jid=$1`, user.jidPtr())
	if err != nil {
		user.log.Warnln("Failed to get user portal keys:", err)
		return nil
	}
	var keys []PortalKey
	for rows.Next() {
		var key PortalKey
		err = rows.Scan(&key.JID, &key.Receiver)
		if err != nil {
			user.log.Warnln("Failed to scan row:", err)
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

func (user *User) GetInCommunityMap() map[PortalKey]bool {
	rows, err := user.db.Query(`SELECT portal_jid, portal_receiver, in_community FROM user_portal WHERE user_jid=$1`, user.jidPtr())
	if err != nil {
		user.log.Warnln("Failed to get user portal keys:", err)
		return nil
	}
	keys := make(map[PortalKey]bool)
	for rows.Next() {
		var key PortalKey
		var inCommunity bool
		err = rows.Scan(&key.JID, &key.Receiver, &inCommunity)
		if err != nil {
			user.log.Warnln("Failed to scan row:", err)
			continue
		}
		keys[key] = inCommunity
	}
	return keys
}
