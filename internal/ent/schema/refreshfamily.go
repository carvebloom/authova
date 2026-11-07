package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type RefreshFamily struct{ ent.Schema }

func (RefreshFamily) Fields() []ent.Field {
	return []ent.Field{field.String("id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("client_id").MaxLen(64).SchemaType(map[string]string{"postgres": "character varying(64)", "mysql": "varchar(64)"}),
		field.String("session_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.Int64("expires_at").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Int16("revoked").SchemaType(map[string]string{"postgres": "smallint", "mysql": "smallint"})}
}
func (RefreshFamily) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "refresh_families"}}
}
