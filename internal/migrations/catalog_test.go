package migrations

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"ariga.io/atlas/sql/migrate"
)

func TestReviewedCatalogIntegrity(t *testing.T) {
	for _, dialect := range []string{"postgres", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			if err := Validate(dialect); err != nil {
				t.Fatal(err)
			}
			dir, err := fs.Sub(Files, "sql/"+dialect)
			if err != nil {
				t.Fatal(err)
			}
			altered := fstest.MapFS{}
			names, _ := fs.Glob(dir, "*")
			for _, name := range names {
				data, err := fs.ReadFile(dir, name)
				if err != nil {
					t.Fatal(err)
				}
				altered[name] = &fstest.MapFile{Data: data}
			}
			altered["001_initial.sql"].Data = append(altered["001_initial.sql"].Data, []byte("\n-- unreviewed change\n")...)
			if err := migrate.Validate(embeddedDir{altered}); !errors.Is(err, migrate.ErrChecksumMismatch) {
				t.Fatal("altered migration accepted", err)
			}
		})
	}
}
