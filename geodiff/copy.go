package geodiff

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"modernc.org/sqlite"
)

// MakeCopySqlite makes a faithful SQLite online backup, including committed WAL
// pages, schema objects and tables without primary keys. The destination is
// installed only after the backup completes. Callers must exclude writers and
// open connections to the destination while replacing it.
func MakeCopySqlite(src, dst string) error {
	if src == "" || dst == "" {
		return fmt.Errorf("copy sqlite: empty source or destination")
	}
	source, err := os.Stat(src)
	if err != nil {
		return err
	}
	if dest, e := os.Stat(dst); e == nil && os.SameFile(source, dest) {
		return fmt.Errorf("copy sqlite: source and destination are the same file")
	}
	db, err := sql.Open("sqlite", sqliteFileURI(src, "ro"))
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	f, err := os.CreateTemp(filepath.Dir(dst), ".sqlite-backup-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	f.Close()
	err = conn.Raw(func(raw any) error {
		backup, err := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		}).NewBackup(tmp)
		if err != nil {
			return err
		}
		_, stepErr := backup.Step(-1)
		return errors.Join(stepErr, backup.Finish())
	})
	if err != nil {
		return fmt.Errorf("copy sqlite backup: %w", err)
	}
	// Closing the backup checkpoints its WAL. Sync the finished file before rename.
	f, err = os.OpenFile(tmp, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	mode := os.FileMode(0644)
	if st, e := os.Stat(dst); e == nil {
		mode = st.Mode().Perm()
	}
	err = errors.Join(f.Chmod(mode), f.Sync())
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if _, e := os.Stat(dst); e == nil {
		old, e := sql.Open("sqlite", sqliteFileURI(dst, "rw"))
		if e != nil {
			return e
		}
		var busy, log, checkpointed int
		e = old.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed)
		closeErr := old.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		if busy != 0 {
			return fmt.Errorf("copy sqlite: destination is busy")
		}
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if e := os.Remove(dst + suffix); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	return os.Rename(tmp, dst)
}

func sqliteFileURI(path, mode string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return u.String() + "?mode=" + mode + "&_pragma=busy_timeout(5000)"
}
