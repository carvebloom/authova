package sqlstore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAtlasPlanningCLI(t *testing.T) {
	if os.Getenv("AUTHOVA_TEST_POSTGRES_DSN") == "" || os.Getenv("AUTHOVA_TEST_MYSQL_DSN") == "" {
		t.Skip("dual-database fixture required")
	}
	root, e := filepath.Abs("../../..")
	if e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(t.TempDir(), "schema-plan")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, filepath.Join(root, "scripts/migration/go.sh"), "build", "-o", binary, "./cmd/schema-plan")
	build.Dir = root
	if b, e := build.CombinedOutput(); e != nil {
		t.Fatalf("planner build: %v %s", e, b)
	}
	for _, dialect := range []string{"postgres", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			run := func(wantOK bool, args ...string) {
				t.Helper()
				c := exec.CommandContext(ctx, binary, append([]string{"--dialect", dialect, "--dsn-env", "AUTHOVA_TEST_MIGRATOR_" + strings.ToUpper(dialect) + "_DSN"}, args...)...)
				if b, e := c.CombinedOutput(); (e == nil) != wantOK {
					t.Fatalf("planner exit mismatch: %v %s", e, b)
				}
			}
			out := filepath.Join(t.TempDir(), "review")
			run(true, "--check")
			run(true, "--out", out)
			record := filepath.Join(out, "review.json")
			run(true, "--verify", record)
			run(false, "--out", out)
			if e = os.WriteFile(filepath.Join(out, "002_unreviewed.sql"), []byte("DROP TABLE apps;\n"), 0600); e != nil {
				t.Fatal(e)
			}
			run(false, "--verify", record)
		})
	}
}
