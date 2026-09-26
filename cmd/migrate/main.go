package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ccar-p/study-platform/internal/config"
	"github.com/ccar-p/study-platform/internal/store"
)

func main() {
	path := flag.String("path", "db/migrations", "migration directory")
	flag.Parse()
	cfg, err := config.LoadWithDevelopmentDefaults()
	if err == nil {
		err = store.Migrate(cfg.DatabaseURL, *path)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
