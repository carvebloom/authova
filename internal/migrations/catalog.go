// Package migrations owns reviewed dialect-specific SQL, including policies and
// other objects not represented by Ent. It does not auto-diff production tables.
package migrations

import (
	"embed"
	"fmt"
	"io/fs"

	"ariga.io/atlas/sql/migrate"
)

//go:embed sql/*/*.sql sql/*/atlas.sum
var Files embed.FS

func Validate(dialect string) error {
	if dialect != "postgres" && dialect != "mysql" {
		return fmt.Errorf("unsupported migration dialect")
	}
	dir, err := fs.Sub(Files, "sql/"+dialect)
	if err != nil {
		return err
	}
	return migrate.Validate(embeddedDir{dir})
}

// embeddedDir implements Atlas Dir over immutable reviewed assets.
type embeddedDir struct{ fs.FS }

func (d embeddedDir) WriteFile(string, []byte) error {
	return fmt.Errorf("embedded migrations are immutable")
}
func (d embeddedDir) Files() ([]migrate.File, error) {
	names, err := fs.Glob(d.FS, "*.sql")
	if err != nil {
		return nil, err
	}
	out := make([]migrate.File, 0, len(names))
	for _, name := range names {
		data, err := fs.ReadFile(d.FS, name)
		if err != nil {
			return nil, err
		}
		out = append(out, migrate.NewLocalFile(name, data))
	}
	return out, nil
}
func (d embeddedDir) Checksum() (migrate.HashFile, error) {
	files, err := d.Files()
	if err != nil {
		return nil, err
	}
	return migrate.NewHashFile(files)
}
