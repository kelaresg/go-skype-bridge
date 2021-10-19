package upgrades

import (
	"database/sql"
)

func init() {
	upgrades[19] = upgrade{"Update user fields varying.", func(tx *sql.Tx, c context) error {
		if c.dialect == Postgres {
			_, err := tx.Exec(`ALTER TABLE "user" ALTER COLUMN skype_token TYPE varchar(1500),
													ALTER COLUMN registration_token TYPE varchar(1500),
													ALTER COLUMN registration_token_str TYPE varchar(1500)`)
			if err != nil {
				return err
			}
		}

		return nil
	}}
}
