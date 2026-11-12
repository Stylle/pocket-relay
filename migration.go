package main

import "database/sql"

// Existing databases and restored backups are upgraded without rewriting messages.
func migrateMessages(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(messages)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, required, primary int
		var name, kind string
		var value any
		if err = rows.Scan(&cid, &name, &kind, &required, &value, &primary); err != nil {
			rows.Close()
			return err
		}
		if name == "concealed" {
			found = true
		}
	}
	err = rows.Err()
	rows.Close() // Close before ALTER: the app deliberately uses one connection.
	if err != nil {
		return err
	}
	if !found {
		_, err = db.Exec(`ALTER TABLE messages ADD COLUMN concealed INTEGER NOT NULL DEFAULT 0`)
	}
	return err
}
