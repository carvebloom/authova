package migrations

import (
	"fmt"
	"io/fs"
	"reflect"
	"strconv"
	"strings"
)

// Names is the contiguous, checksum-validated compiled migration catalog.
// New reviewed files need no dispatcher code changes. Historical names stay fixed.
func Names(dialect string) ([]string, error) {
	if dialect != "postgres" && dialect != "mysql" {
		return nil, fmt.Errorf("unsupported migration dialect")
	}
	names, err := fs.Glob(Files, "sql/"+dialect+"/*.sql")
	if err != nil {
		return nil, err
	}
	for i, n := range names {
		names[i] = strings.TrimPrefix(n, "sql/"+dialect+"/")
	}
	if err = validateVersions(names); err != nil {
		return nil, err
	}
	return names, nil
}
func validateVersions(names []string) error {
	if len(names) == 0 {
		return fmt.Errorf("empty migration catalog")
	}
	for i, n := range names {
		prefix, _, ok := strings.Cut(n, "_")
		v, e := strconv.Atoi(prefix)
		if !ok || e != nil || len(prefix) != 3 || v != i+1 {
			return fmt.Errorf("migration catalog must be contiguous: %q at version %d", n, i+1)
		}
	}
	return nil
}
func LatestVersion() int {
	pg, e := Names("postgres")
	if e != nil {
		panic(e)
	}
	my, e := Names("mysql")
	if e != nil {
		panic(e)
	}
	if !reflect.DeepEqual(pg, my) {
		panic("PostgreSQL/MySQL migration catalogs disagree")
	}
	return len(pg)
}
