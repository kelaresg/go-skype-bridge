package upgrades

import (
	"database/sql"
)

func init() {
	upgrades[20] = upgrade{"Add password and username column to user table.", func(tx *sql.Tx, c context) error {
		if c.dialect == Postgres {
			_, err := tx.Exec(`ALTER TABLE "user" ADD COLUMN password VARCHAR(255)`)
			if err != nil {
				return err
			}
		}

		if c.dialect == Postgres {
			_, err := tx.Exec(`ALTER TABLE "user" ADD COLUMN username VARCHAR(255)`)
			if err != nil {
				return err
			}
		}

		return nil
	}}
}
