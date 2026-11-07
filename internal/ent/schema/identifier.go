package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type Identifier struct{ ent.Schema }

func (Identifier) Fields() []ent.Field {
	return []ent.Field{field.String("id").StorageKey("digest").MaxLen(64).SchemaType(map[string]string{"postgres": "character varying(64)", "mysql": "varchar(64)"}),
		field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("kind").MaxLen(16).SchemaType(map[string]string{"postgres": "character varying(16)", "mysql": "varchar(16)"}),
		field.String("user_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"})}
}
func (Identifier) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "identifiers"}}
}
