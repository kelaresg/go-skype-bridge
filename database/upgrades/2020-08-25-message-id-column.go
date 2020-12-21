package upgrades

import (
	"database/sql"
)

func init() {
	upgrades[17] = upgrade{"Add id column to messages", func(tx *sql.Tx, ctx context) error {
		_, err := tx.Exec(`ALTER TABLE message ADD COLUMN id CHAR(13) DEFAULT ''`)
		if err != nil {
			return err
		}
		return nil
	}}
}
