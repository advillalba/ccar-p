package store

import (
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func Migrate(databaseURL, path string) error {
	migration, err := migrate.New("file://"+path, databaseURL)
	if err != nil {
		return fmt.Errorf("initialize migrations: %w", err)
	}
	defer migration.Close()
	if err := migration.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
