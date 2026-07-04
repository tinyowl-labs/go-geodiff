# go-geodiff

Go port of [geodiff](https://github.com/MerginMaps/geodiff) — a library for creating, applying, and merging diffs of GeoPackage and SQLite databases.

Originally by [Lutra Consulting](https://www.lutraconsulting.co.uk/) (MIT). This port preserves the binary changeset format used by the [Mergin Maps](https://merginmaps.com/) / QField ecosystem.

## Usage

```go
import "github.com/tinyowl-labs/go-geodiff/geodiff"

// Create a binary diff between two GPKG files
err := geodiff.CreateChangeset("base.gpkg", "modified.gpkg", "changes.diff")

// Apply a diff to a GPKG (in-place)
err = geodiff.ApplyChangeset("target.gpkg", "changes.diff")

// Rebase local changes on top of remote
err = geodiff.Rebase("base.gpkg", "remote.gpkg", "local.gpkg", "conflicts.json")

// Export changeset as JSON
err = geodiff.ListChanges("changes.diff", "changes.json")

// Count changes
count, err := geodiff.ChangesCount("changes.diff")

// Invert a changeset
err = geodiff.InvertChangeset("changes.diff", "changes_inv.diff")
```

## Relationship to upstream

This is a careful, function-by-function port of the C++ geodiff library (v2.3.0), targeting only the SQLite/GeoPackage driver. The Postgres driver and CLI tool are excluded.

Binary changeset files produced by go-geodiff are **byte-identical** to those produced by the C++ `geodiff` CLI — verified by cross-validation tests in `crossval/`.

## What's included

| Package | Purpose |
|---------|---------|
| `geodiff` | Public API — `CreateChangeset`, `ApplyChangeset`, `Rebase`, `InvertChangeset`, `ListChanges`, `MakeCopy`, `Schema`, `DumpData`, WKB header stripping |
| `driver` | `SqliteDriver` — ATTACH-based SQL diffing. `Rebase` — 3-way merge engine |
| `changeset` | Binary changeset format — reader, writer, types, invert, concat, JSON export |
| `schema` | `TableSchema`, `TableColumnInfo`, `CrsDefinition` — database introspection |
| `varint` | SQLite variable-length integer encoding |
| `crossval` | Cross-validation against C++ geodiff binary (CI-only) |

## Dependencies

Zero beyond Go's standard library plus two well-established packages already in the TinyOwl ecosystem:

- [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite) — pure Go SQLite (no CGo)
- [`github.com/twpayne/go-geom`](https://pkg.go.dev/github.com/twpayne/go-geom) — WKB geometry parsing

No system dependencies. Single binary.

## Testing

```bash
# Run all tests (162 tests, ~0.1s)
go test ./...

# Cross-validate against C++ geodiff binary
GEODIFF_CPP_BIN=/path/to/geodiff go test ./crossval/ -v
```

## License

MIT — same as the upstream [geodiff](https://github.com/MerginMaps/geodiff) library.

Copyright (c) 2019 Lutra Consulting Ltd.
Copyright (c) 2026 TinyOwl Contributors
