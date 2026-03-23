package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/uptrace/bun"
)

// ---------------------------------------------------------------------------
// Command
// ---------------------------------------------------------------------------

func runCleanDeprecated(args []string) {
	var (
		dsn    string
		driver = "sqlite"
	)

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--db":
			i++
			if i >= len(args) {
				fatalf("clean deprecated: --db requires a value")
			}
			dsn = args[i]
		case "--driver":
			i++
			if i >= len(args) {
				fatalf("clean deprecated: --driver requires a value")
			}
			driver = args[i]
		default:
			fatalf("clean deprecated: unknown flag %q", args[i])
		}
	}

	if dsn == "" {
		fatalf("clean deprecated: --db is required")
	}

	db, err := openDB(driver, dsn)
	if err != nil {
		fatalf("clean deprecated: %v", err)
	}
	defer db.Close()

	if err := cleanDeprecated(db, driver); err != nil {
		fatalf("clean deprecated: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Clean logic
// ---------------------------------------------------------------------------

type collectionRow struct {
	Slug             string          `bun:"slug"`
	DeprecatedFields json.RawMessage `bun:"deprecated_fields"`
}

func cleanDeprecated(db *bun.DB, driver string) error {
	ctx := context.Background()

	var rows []collectionRow
	err := db.NewSelect().
		TableExpr("reverb_collections").
		Column("slug", "deprecated_fields").
		Where("deprecated_fields IS NOT NULL").
		Scan(ctx, &rows)
	if err != nil {
		return fmt.Errorf("query collections: %w", err)
	}

	type target struct {
		slug   string
		fields []string
	}
	var targets []target
	for _, r := range rows {
		var fields []string
		if err := json.Unmarshal(r.DeprecatedFields, &fields); err != nil || len(fields) == 0 {
			continue
		}
		targets = append(targets, target{slug: r.Slug, fields: fields})
	}

	if len(targets) == 0 {
		fmt.Println("No deprecated fields found.")
		return nil
	}

	totalEntries := 0
	type summary struct {
		target     target
		entryCount int
	}
	summaries := make([]summary, 0, len(targets))

	for _, t := range targets {
		var count int
		row := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM reverb_collection_entries WHERE collection_slug = ?", t.slug)
		if err := row.Scan(&count); err != nil {
			return fmt.Errorf("count entries for %q: %w", t.slug, err)
		}
		summaries = append(summaries, summary{target: t, entryCount: count})
		totalEntries += count
	}

	for _, s := range summaries {
		fmt.Printf("Collection: %s\n", s.target.slug)
		fmt.Printf("  Deprecated fields: %s\n", strings.Join(s.target.fields, ", "))
		fmt.Printf("  Affected entries: %d\n", s.entryCount)
	}

	fmt.Print("\nPurge deprecated field data from all listed collections? [y/N]: ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	answer := strings.TrimSpace(scanner.Text())
	if answer != "y" && answer != "Y" {
		fmt.Println("Aborted.")
		return nil
	}

	totalFieldsPurged := 0
	totalCollections := 0

	for _, s := range summaries {
		for _, field := range s.target.fields {
			var (
				sqlStr string
				param1 interface{}
			)
			switch driver {
			case "postgres":
				sqlStr = "UPDATE reverb_collection_entries SET data = data - $1 WHERE collection_slug = $2"
				param1 = field
			case "mysql":
				sqlStr = "UPDATE reverb_collection_entries SET data = JSON_REMOVE(data, ?) WHERE collection_slug = ?"
				param1 = "$." + field
			default:
				sqlStr = "UPDATE reverb_collection_entries SET data = json_remove(data, ?) WHERE collection_slug = ?"
				param1 = "$." + field
			}
			_, err := db.ExecContext(ctx, sqlStr, param1, s.target.slug)
			if err != nil {
				return fmt.Errorf("purge field %q from %q: %w", field, s.target.slug, err)
			}
			totalFieldsPurged++
		}

		_, err := db.ExecContext(ctx,
			"UPDATE reverb_collections SET deprecated_fields = NULL WHERE slug = ?",
			s.target.slug)
		if err != nil {
			return fmt.Errorf("clear deprecated_fields for %q: %w", s.target.slug, err)
		}
		totalCollections++
	}

	fmt.Printf("Purged %d fields from %d entries across %d collections.\n",
		totalFieldsPurged, totalEntries, totalCollections)
	return nil
}
