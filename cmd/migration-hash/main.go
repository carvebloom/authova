// migration-hash updates Atlas checksums after manual review of versioned SQL.
package main

import (
	"ariga.io/atlas/sql/migrate"
	"log"
)

func main() {
	for _, name := range []string{"postgres", "mysql"} {
		dir, err := migrate.NewLocalDir("internal/migrations/sql/" + name)
		if err != nil {
			log.Fatal(err)
		}
		sum, err := dir.Checksum()
		if err != nil {
			log.Fatal(err)
		}
		if err = migrate.WriteSumFile(dir, sum); err != nil {
			log.Fatal(err)
		}
	}
}
