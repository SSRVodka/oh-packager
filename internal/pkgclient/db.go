package pkgclient

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB wrapper
type DB struct {
	*sql.DB
}

// Installed row
type Installed struct {
	Name    string
	Version string
	Arch    string
	Prefix  string
	Path    string
	When    time.Time
}

// OpenDB opens/creates database and ensures schema.
func OpenDB(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dbconn, err := sql.Open("sqlite3", path+"?_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db := &DB{dbconn}
	if err := db.ensureSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) ensureSchema() error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS installed (
		name TEXT NOT NULL,
		version TEXT NOT NULL,
		arch TEXT,
		prefix TEXT NOT NULL,
		path TEXT NOT NULL,
		installed_at DATETIME,
		PRIMARY KEY (name, prefix)
	)`)
	return err
}

func (db *DB) InsertInstalled(name, version, arch, prefix, path string) error {
	_, err := db.Exec(`INSERT OR REPLACE INTO installed(name,version,arch,prefix,path,installed_at) VALUES (?,?,?,?,?,?)`,
		name, version, arch, prefix, path, time.Now().UTC())
	return err
}

func (db *DB) GetInstalled(name, prefix string) (*Installed, error) {
	row := db.QueryRow(`SELECT name,version,arch,prefix,path,installed_at FROM installed WHERE name=? AND prefix=?`, name, prefix)
	var it Installed
	var t string
	err := row.Scan(&it.Name, &it.Version, &it.Arch, &it.Prefix, &it.Path, &t)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	it.When, _ = time.Parse(time.RFC3339Nano, t)
	return &it, nil
}

func (db *DB) DeleteInstalled(name, prefix string) error {
	_, err := db.Exec(`DELETE FROM installed WHERE name=? AND prefix=?`, name, prefix)
	return err
}
