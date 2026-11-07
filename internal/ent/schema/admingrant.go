package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type AdminGrant struct{ ent.Schema }

func (AdminGrant) Fields() []ent.Field {
	return []ent.Field{field.String("id").StorageKey("user_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("role").MaxLen(16).SchemaType(map[string]string{"postgres": "character varying(16)", "mysql": "varchar(16)"})}
}
func (AdminGrant) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "admin_grants"}}
}
