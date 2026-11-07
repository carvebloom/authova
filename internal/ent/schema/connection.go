package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type Connection struct{ ent.Schema }

func (Connection) Fields() []ent.Field {
	return []ent.Field{field.String("id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.Bytes("payload").SchemaType(map[string]string{"postgres": "bytea", "mysql": "longblob"})}
}
func (Connection) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "connections"}}
}
