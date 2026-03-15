package sqliteutil_test

import (
	"testing"

	"github.com/Sixeight/kiroku/internal/sqliteutil"
)

func TestOpenConfiguresWALAndBusyTimeout(t *testing.T) {
	path := t.TempDir() + "/busy.sqlite"

	db, err := sqliteutil.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE sample(id INTEGER PRIMARY KEY, name TEXT); INSERT INTO sample(name) VALUES ('a');`); err != nil {
		t.Fatal(err)
	}

	var journalMode string
	if err := db.QueryRow(`PRAGMA journal_mode;`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}

	if got, want := journalMode, "wal"; got != want {
		t.Fatalf("journal_mode = %q, want %q", got, want)
	}

	var busyTimeout int
	if err := db.QueryRow(`PRAGMA busy_timeout;`).Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}

	if got, want := busyTimeout, 5000; got != want {
		t.Fatalf("busy_timeout = %d, want %d", got, want)
	}
}
