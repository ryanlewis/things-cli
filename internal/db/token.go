package db

import (
	"database/sql"
	"fmt"
)

func (d *DB) GetAuthToken() (string, error) {
	var token sql.NullString
	err := d.db.QueryRow("SELECT uriSchemeAuthenticationToken FROM TMSettings LIMIT 1").Scan(&token)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("querying auth token: %w", err)
	}
	return token.String, nil
}
