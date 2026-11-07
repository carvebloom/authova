package schemaplan

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"ariga.io/atlas/sql/mysql"
	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
)

type Report struct {
	Format              int          `json:"format"`
	Dialect             string       `json:"dialect"`
	SourceSHA256        string       `json:"source_sha256"`
	TargetSHA256        string       `json:"target_sha256"`
	Protected           []Protection `json:"protected"`
	Statements          []string     `json:"statements"`
	SQLSHA256           string       `json:"sql_sha256"`
	ReviewedDestructive bool         `json:"explicit_destructive_review"`
}

func hash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func stateHash(dialect string, s *schema.Schema) (string, error) {
	var data []byte
	var err error
	if dialect == "postgres" {
		data, err = postgres.MarshalHCL(s)
	} else {
		data, err = mysql.MarshalHCL(s)
	}
	return hash(data), err
}
func BuildReport(ctx context.Context, tx *sql.Tx, dialect, name string, reviewDestructive bool) (*Report, error) {
	driver, err := Open(tx, dialect)
	if err != nil {
		return nil, err
	}
	current, err := driver.InspectSchema(ctx, name, &schema.InspectOptions{Mode: schema.InspectTables})
	if err != nil {
		return nil, err
	}
	desired, err := Desired(ctx, tx, dialect, name)
	if err != nil {
		return nil, err
	}
	protected, err := inspectProtection(ctx, tx, dialect, name, reviewDestructive)
	if err != nil {
		return nil, err
	}
	plan, err := Diff(ctx, driver, current, desired, reviewDestructive)
	if err != nil {
		return nil, err
	}
	sourceHash, err := stateHash(dialect, current)
	if err != nil {
		return nil, err
	}
	targetHash, err := stateHash(dialect, desired)
	if err != nil {
		return nil, err
	}
	r := &Report{Format: 1, Dialect: dialect, SourceSHA256: sourceHash, TargetSHA256: targetHash, Protected: protected, Statements: []string{}, ReviewedDestructive: reviewDestructive}
	for _, c := range plan.Changes {
		r.Statements = append(r.Statements, c.Cmd)
	}
	r.SQLSHA256 = hash([]byte(r.SQL()))
	return r, nil
}
func (r *Report) SQL() string {
	if len(r.Statements) == 0 {
		return ""
	}
	return "-- Generated review candidate from pinned Ent + Atlas OSS; apply only after review.\n" + strings.Join(r.Statements, ";\n") + ";\n"
}
func Verify(ctx context.Context, tx *sql.Tx, dialect, name string, record []byte) error {
	var old Report
	if err := json.Unmarshal(record, &old); err != nil {
		return err
	}
	if old.Format != 1 || old.Dialect != dialect {
		return fmt.Errorf("plan format/dialect mismatch")
	}
	if old.SQLSHA256 != hash([]byte(old.SQL())) {
		return fmt.Errorf("plan statements checksum mismatch")
	}
	now, err := BuildReport(ctx, tx, dialect, name, old.ReviewedDestructive)
	if err != nil {
		return err
	}
	if old.SourceSHA256 != now.SourceSHA256 || old.TargetSHA256 != now.TargetSHA256 || !reflect.DeepEqual(old.Protected, now.Protected) || !reflect.DeepEqual(old.Statements, now.Statements) {
		return fmt.Errorf("stale plan: database, Ent model or protected objects changed; regenerate and review")
	}
	return nil
}
