package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ccar-p/study-platform/internal/config"
	"github.com/ccar-p/study-platform/internal/store"
)

func main() {
	cfg, err := config.LoadWithDevelopmentDefaults()
	var database *store.Store
	if err == nil {
		database, err = store.Open(context.Background(), cfg.DatabaseURL)
	}
	if err == nil {
		defer database.Close()
		err = database.SeedDevelopment(context.Background(), cfg.AppEnv, store.SeedAdmin{Email: os.Getenv("SEED_ADMIN_EMAIL"), Password: os.Getenv("SEED_ADMIN_PASSWORD"), DisplayName: os.Getenv("SEED_ADMIN_DISPLAY_NAME")})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
