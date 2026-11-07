package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type AuthorizationCode struct{ ent.Schema }

func (AuthorizationCode) Fields() []ent.Field {
	return []ent.Field{field.String("id").MaxLen(64).StorageKey("digest").SchemaType(map[string]string{"postgres": "character varying(64)", "mysql": "varchar(64)"}),
		field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("flow_id").MaxLen(64).SchemaType(map[string]string{"postgres": "character varying(64)", "mysql": "varchar(64)"}),
		field.Int64("expires_at").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Int16("used").SchemaType(map[string]string{"postgres": "smallint", "mysql": "smallint"})}
}
func (AuthorizationCode) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "codes"}}
}
