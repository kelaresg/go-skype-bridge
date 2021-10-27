package upgrades

import (
	"database/sql"
)

func init() {
	upgrades[0] = upgrade{"Initial schema", func(tx *sql.Tx, ctx context) error {
		_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS portal (
			jid      VARCHAR(255),
			receiver VARCHAR(255),
			mxid     VARCHAR(255) UNIQUE,

			name   VARCHAR(255) NOT NULL,
			topic  VARCHAR(255) NOT NULL,
			avatar VARCHAR(255) NOT NULL,

			PRIMARY KEY (jid, receiver)
		)`)
		if err != nil {
			return err
		}

		_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS puppet (
			jid          VARCHAR(255) PRIMARY KEY,
			avatar       VARCHAR(255),
			displayname  VARCHAR(255),
			name_quality SMALLINT
		)`)
		if err != nil {
			return err
		}

		_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS "user" (
			mxid VARCHAR(255) PRIMARY KEY,
			jid  VARCHAR(255) UNIQUE,

			management_room VARCHAR(255),

			endpoint_id    VARCHAR(255),
			skype_token VARCHAR(255),
			registration_token VARCHAR(255),
			registration_token_str      VARCHAR(255),
			location_host      VARCHAR(255)
		)`)
		if err != nil {
			return err
		}

		_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS message (
			chat_jid      VARCHAR(255),
			chat_receiver VARCHAR(255),
			jid           VARCHAR(255),
			mxid          VARCHAR(255) NOT NULL UNIQUE,
			sender        VARCHAR(255) NOT NULL,
			content       bytea        NOT NULL,

			PRIMARY KEY (chat_jid, chat_receiver, jid),
			FOREIGN KEY (chat_jid, chat_receiver) REFERENCES portal(jid, receiver) ON DELETE CASCADE
		)`)
		if err != nil {
			return err
		}

		return nil
	}}
}
