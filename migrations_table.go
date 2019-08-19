package click_mig

import (
	"github.com/jmoiron/sqlx"
	"log"
	"time"
)

type migrationEntry struct {
	Id          int32     `db:"id"`
	Name        string    `db:"name"`
	DateApplied time.Time `db:"applied"`
}

func createMigrationTable(client client) error {
	_, err := client.Exec(`
	CREATE TABLE migrations(
		id Int32,
		name String,
		applied DateTime
	) engine=TinyLog()
	`)

	return err
}

func setMigrationApplied(id int32, name string, tx *sqlx.Tx) error {
	mig := migrationEntry{Id: id, Name: name, DateApplied: time.Now()}

	_, err := tx.NamedExec("INSERT INTO migrations (id, name, applied) VALUES (:id, :name, :applied)", &mig)
	return err
}

func isMigrationTableExists(client client) bool {
	r, err := client.Queryx("SHOW TABLES LIKE 'migrations'")
	if err != nil {
		log.Fatalf("Cannot connect to node: %v", err)
		return false
	}
	defer r.Close()
	return r.Next()
}

func listMigrationEntries(client client) (*[]migrationEntry, error) {
	migrations := []migrationEntry{}
	if err := client.Select(&migrations, "SELECT * FROM migrations ORDER BY id ASC"); err != nil {
		return nil, err
	}

	return &migrations, nil
}
