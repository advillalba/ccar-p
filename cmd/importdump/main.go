package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ccar-p/study-platform/internal/config"
	"github.com/ccar-p/study-platform/internal/store"
)

func main() {
	dir := "db/public-dump"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	cfg, err := config.Load()
	var database *store.Store
	if err == nil {
		database, err = store.Open(context.Background(), cfg.DatabaseURL)
	}
	if err == nil {
		defer database.Close()
		var counts map[string]int
		counts, err = database.ImportPublicDump(context.Background(), dir)
		if err == nil {
			for table, count := range counts {
				fmt.Printf("%s: %d rows inserted\n", table, count)
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
