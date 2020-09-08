package database

import (
	"bytes"
	"database/sql"
	"encoding/json"
	skype "github.com/kelaresg/go-skypeapi"

	log "maunium.net/go/maulogger/v2"

	"github.com/kelaresg/matrix-skype/types"
	"maunium.net/go/mautrix/id"
)

type MessageQuery struct {
	db  *Database
	log log.Logger
}

func (mq *MessageQuery) New() *Message {
	return &Message{
		db:  mq.db,
		log: mq.log,
	}
}

func (mq *MessageQuery) GetAll(chat PortalKey) (messages []*Message) {
	rows, err := mq.db.Query("SELECT id, chat_jid, chat_receiver, jid, mxid, sender, timestamp, content FROM message WHERE chat_jid=$1 AND chat_receiver=$2", chat.JID, chat.Receiver)
	if err != nil || rows == nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		messages = append(messages, mq.New().Scan(rows))
	}
	return
}

func (mq *MessageQuery) GetByJID(chat PortalKey, jid types.SkypeMessageID) *Message {
	return mq.get("SELECT id, chat_jid, chat_receiver, jid, mxid, sender, timestamp, content " +
		"FROM message WHERE chat_jid=$1 AND jid=$2", chat.JID, jid)
}

func (mq *MessageQuery) oldGetByJID(chat PortalKey, jid types.SkypeMessageID) *Message {
	return mq.get("SELECT id, chat_jid, chat_receiver, jid, mxid, sender, timestamp, content " +
		"FROM message WHERE chat_jid=$1 AND chat_receiver=$2 AND jid=$3", chat.JID, chat.Receiver, jid)
}

func (mq *MessageQuery) GetByMXID(mxid id.EventID) *Message {
	return mq.get("SELECT id, chat_jid, chat_receiver, jid, mxid, sender, timestamp, content " +
		"FROM message WHERE mxid=$1", mxid)
}

func (mq *MessageQuery) GetLastInChat(chat PortalKey) *Message {
	msg := mq.get("SELECT id, chat_jid, chat_receiver, jid, mxid, sender, timestamp, content " +
		"FROM message WHERE chat_jid=$1 AND chat_receiver=$2 ORDER BY timestamp DESC LIMIT 1", chat.JID, chat.Receiver)
	if msg == nil || msg.Timestamp == 0 {
		// Old db, we don't know what the last message is.
		return nil
	}
	return msg
}

func (mq *MessageQuery) get(query string, args ...interface{}) *Message {
	row := mq.db.QueryRow(query, args...)
	if row == nil {
		return nil
	}
	return mq.New().Scan(row)
}

type Message struct {
	db  *Database
	log log.Logger

	ID        types.SkypeMessageID
	Chat      PortalKey
	JID       types.SkypeMessageID
	MXID      id.EventID
	Sender    types.SkypeID
	Timestamp uint64
	Content   *skype.Resource
}

func (msg *Message) Scan(row Scannable) *Message {
	var content []byte
	err := row.Scan(&msg.ID, &msg.Chat.JID, &msg.Chat.Receiver, &msg.JID, &msg.MXID, &msg.Sender, &msg.Timestamp, &content)
	if err != nil {
		if err != sql.ErrNoRows {
			msg.log.Errorln("Database scan failed:", err)
		}
		return nil
	}

	msg.decodeBinaryContent(content)

	return msg
}

func (msg *Message) decodeBinaryContent(content []byte) {
	msg.Content = &skype.Resource{}
	reader := bytes.NewReader(content)
	dec := json.NewDecoder(reader)
	err := dec.Decode(&msg.Content)
	if err != nil {
		msg.log.Warnln("Failed to decode message content:", err)
	}
}

func (msg *Message) encodeBinaryContent() []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	err := enc.Encode(msg.Content)
	if err != nil {
		msg.log.Warnln("Failed to encode message content:", err)
	}
	return buf.Bytes()
}

func (msg *Message) Insert() {
	_, err := msg.db.Exec("INSERT INTO message (id, chat_jid, chat_receiver, jid, mxid, sender, timestamp, content) " +
		"VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
		msg.ID, msg.Chat.JID, msg.Chat.Receiver, msg.JID, msg.MXID, msg.Sender, msg.Timestamp, msg.encodeBinaryContent())
	if err != nil {
		msg.log.Warnfln("Failed to insert %s@%s: %v", msg.Chat, msg.JID, err)
	}
}

func (msg *Message) Delete() {
	_, err := msg.db.Exec("DELETE FROM message WHERE chat_jid=$1 AND chat_receiver=$2 AND jid=$3", msg.Chat.JID, msg.Chat.Receiver, msg.JID)
	if err != nil {
		msg.log.Warnfln("Failed to delete %s@%s: %v", msg.Chat, msg.JID, err)
	}
}

func (msg *Message) UpdateIDByJID(id string) {
	_, err := msg.db.Exec("UPDATE message SET id=$1 WHERE chat_jid=$2 AND jid=$3", id, msg.Chat.JID, msg.JID)
	if err != nil {
		msg.log.Warnfln("Failed to UpdateIDByJID %s@%s: %v", msg.Chat.JID, msg.JID, err)
	}
}
