package schemaplan

import (
	"ariga.io/atlas/sql/schema"
	"testing"
)

func TestDestructiveReviewFence(t *testing.T) {
	for _, change := range []schema.Change{&schema.DropTable{T: schema.NewTable("users")}, &schema.DropColumn{C: schema.NewColumn("payload")}, &schema.DropIndex{I: schema.NewIndex("client_unique")}, &schema.ModifyColumn{From: schema.NewColumn("id"), To: schema.NewColumn("id")}, &schema.DropPrimaryKey{P: schema.NewPrimaryKey()}, &schema.AddTable{T: schema.NewTable("new_tenant_table")}} {
		if e := reviewChanges([]schema.Change{&schema.ModifyTable{T: schema.NewTable("users"), Changes: []schema.Change{change}}}); e == nil {
			t.Fatalf("change %T escaped review fence", change)
		}
	}
	required := schema.NewColumn("required").SetType(&schema.StringType{T: "varchar", Size: 64})
	if e := reviewChanges([]schema.Change{&schema.AddColumn{C: required}}); e == nil {
		t.Fatal("non-null addition without backfill accepted")
	}
	nullable := schema.NewColumn("optional").SetType(&schema.StringType{T: "varchar", Size: 64}).SetNull(true)
	if e := reviewChanges([]schema.Change{&schema.AddColumn{C: nullable}}); e != nil {
		t.Fatal(e)
	}
}
func TestCrossAppForeignKeyRefused(t *testing.T) {
	s := schema.New("public")
	users := schema.NewTable("users").AddColumns(schema.NewStringColumn("app_id", "varchar"), schema.NewStringColumn("id", "varchar"))
	sessions := schema.NewTable("sessions").AddColumns(schema.NewStringColumn("app_id", "varchar"), schema.NewStringColumn("user_id", "varchar"))
	id, _ := users.Column("id")
	uid, _ := sessions.Column("user_id")
	sessions.AddForeignKeys(schema.NewForeignKey("unsafe").SetRefTable(users).AddColumns(uid).AddRefColumns(id))
	s.AddTables(users, sessions)
	if e := ValidateBoundForeignKeys(s); e == nil {
		t.Fatal("App-unbound FK accepted")
	}
}
