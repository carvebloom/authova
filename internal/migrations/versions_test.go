package migrations

import "testing"

func TestVersionCatalog(t *testing.T) {
	for _, test := range []struct {
		names []string
		valid bool
	}{{[]string{"001_a.sql", "002_b.sql", "003_future.sql"}, true}, {[]string{"001_a.sql", "003_b.sql"}, false}, {[]string{"001_a.sql", "001_b.sql"}, false}, {[]string{"1_a.sql"}, false}, {nil, false}} {
		if e := validateVersions(test.names); (e == nil) != test.valid {
			t.Fatalf("%v: %v", test.names, e)
		}
	}
	names, e := Names("postgres")
	if e != nil || len(names) == 0 || names[0] != "001_initial.sql" {
		t.Fatal("missing v1 initial baseline", e)
	}
	if LatestVersion() != len(names) {
		t.Fatal("catalog version discovery differs")
	}
}
