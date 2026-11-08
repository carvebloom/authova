// schema-plan creates review artifacts only. It never executes generated DDL.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/carvebloom/authova/internal/diagnostic"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/carvebloom/authova/internal/migrations"
	"github.com/carvebloom/authova/internal/schemaplan"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func run() error {
	dialect := flag.String("dialect", "", "postgres or mysql")
	env := flag.String("dsn-env", "AUTHOVA_PLAN_DATABASE_URL", "environment variable containing read-only source DSN")
	out := flag.String("out", "", "new directory for review artifacts")
	name := flag.String("name", "ent_change", "migration name")
	check := flag.Bool("check", false, "fail if Ent differs from current schema; no files written")
	verify := flag.String("verify", "", "verify an existing review.json against current database/model")
	destructive := flag.Bool("review-destructive", false, "include destructive/backfill changes for explicit review, never apply")
	flag.Parse()
	if *dialect != "postgres" && *dialect != "mysql" {
		return fmt.Errorf("--dialect must be postgres or mysql")
	}
	dsn := os.Getenv(*env)
	if dsn == "" {
		return fmt.Errorf("source DSN environment is missing")
	}
	driver := *dialect
	if driver == "postgres" {
		driver = "pgx"
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return diagnostic.Wrap(err, "storage", "connect")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return diagnostic.Wrap(err, "storage", "begin")
	}
	defer tx.Rollback()
	schemaName := "public"
	if *dialect == "mysql" {
		if err = tx.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&schemaName); err != nil {
			return err
		}
	}
	if *verify != "" {
		data, e := os.ReadFile(*verify)
		if e != nil {
			return e
		}
		if e = schemaplan.Verify(ctx, tx, *dialect, schemaName, data); e != nil {
			return e
		}
		var report schemaplan.Report
		if e = json.Unmarshal(data, &report); e != nil {
			return e
		}
		candidates, e := filepath.Glob(filepath.Join(filepath.Dir(*verify), "*.sql"))
		if e != nil {
			return e
		}
		if len(report.Statements) > 0 {
			if len(candidates) != 1 {
				return fmt.Errorf("expected exactly one SQL candidate")
			}
			sql, e := os.ReadFile(candidates[0])
			if e != nil {
				return e
			}
			if !bytes.Equal(sql, []byte(report.SQL())) {
				return fmt.Errorf("SQL candidate differs from reviewed statements")
			}
		} else if len(candidates) != 0 {
			return fmt.Errorf("no-op review must not contain a SQL migration")
		}
		fmt.Println("Review plan matches current schema, Ent model and protected objects.")
		return nil
	}
	report, err := schemaplan.BuildReport(ctx, tx, *dialect, schemaName, *destructive)
	if err != nil {
		return err
	}
	if *check {
		if len(report.Statements) > 0 {
			return fmt.Errorf("schema differs from Ent physical model: %d statements", len(report.Statements))
		}
		fmt.Println("Ent physical model matches database; protected objects checked.")
		return nil
	}
	if *out == "" {
		return fmt.Errorf("--out is required unless --check/--verify is used")
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`).MatchString(*name) {
		return fmt.Errorf("invalid migration name")
	}
	if err = os.Mkdir(*out, 0755); err != nil {
		return fmt.Errorf("review directory must be new: %w", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(*out, "review.json"), append(data, '\n'), 0644); err != nil {
		return err
	}
	if len(report.Statements) > 0 {
		names, e := migrations.Names(*dialect)
		if e != nil {
			return e
		}
		file := fmt.Sprintf("%03d_%s.sql", len(names)+1, *name)
		if err = os.WriteFile(filepath.Join(*out, file), []byte(report.SQL()), 0644); err != nil {
			return err
		}
	}
	fmt.Printf("Generated %s review: %d statements. No DDL applied.\n", *dialect, len(report.Statements))
	return nil
}
func main() {
	if err := run(); err != nil {
		diagnostic.Log(context.Background(), "storage", "query", err)
		fmt.Fprintln(os.Stderr, "schema-plan: operation failed")
		os.Exit(1)
	}
}
