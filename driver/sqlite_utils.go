package driver

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// isGeoPackage checks whether the given SQLite database is a GeoPackage.
func isGeoPackage(db *sql.DB) bool {
	rows, err := db.QueryContext(context.Background(), "SELECT name FROM sqlite_master WHERE type='table' AND name='gpkg_contents'")
	if err != nil {
		return false
	}
	defer rows.Close()
	return rows.Next()
}

// sqliteTriggers returns user-defined triggers (excluding GPKG system triggers).
func sqliteTriggers(db *sql.DB) (names []string, cmds []string, err error) {
	rows, err := db.QueryContext(context.Background(), "SELECT name, sql FROM sqlite_master WHERE type = 'trigger'")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list triggers: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name, sqlStr string
		if err := rows.Scan(&name, &sqlStr); err != nil {
			continue
		}
		if name == "" || sqlStr == "" {
			continue
		}
		// Skip GPKG system triggers
		if strings.HasPrefix(name, "gpkg_") {
			continue
		}
		if strings.HasPrefix(name, "rtree_") {
			continue
		}
		if strings.HasPrefix(name, "trigger_insert_feature_count_") {
			continue
		}
		if strings.HasPrefix(name, "trigger_delete_feature_count_") {
			continue
		}
		names = append(names, name)
		cmds = append(cmds, sqlStr)
	}
	return names, cmds, rows.Err()
}

// dropTriggers drops the given trigger names from the database.
func dropTriggers(db *sql.DB, names []string) error {
	for _, name := range names {
		sqlStr := fmt.Sprintf("DROP TRIGGER \"%s\"", strings.ReplaceAll(name, "\"", "\"\""))
		if _, err := db.ExecContext(context.Background(), sqlStr); err != nil {
			return fmt.Errorf("failed to drop trigger %s: %w", name, err)
		}
	}
	return nil
}

// createTriggers recreates triggers from their SQL definitions.
func createTriggers(db *sql.DB, cmds []string) error {
	for _, cmd := range cmds {
		if _, err := db.ExecContext(context.Background(), cmd); err != nil {
			return fmt.Errorf("failed to recreate trigger: %w", err)
		}
	}
	return nil
}
