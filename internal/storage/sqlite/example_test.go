package sqlite_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"bord/internal/storage/sqlite"
)

func ExampleOpen() {
	dir, err := os.MkdirTemp("", "bord-sqlite-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	db, err := sqlite.Open(filepath.Join(dir, "board.db"))
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		panic(err)
	}
	fmt.Println(sqlite.Ready(context.Background(), db) == nil)
	// Output: true
}
