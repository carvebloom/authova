package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type SigningKey struct{ ent.Schema }

func (SigningKey) Fields() []ent.Field {
	return []ent.Field{field.String("id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.Int16("current_key").SchemaType(map[string]string{"postgres": "smallint", "mysql": "smallint"}),
		field.Int64("retire_at").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Int64("revoked_at").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}).Annotations(entsql.DefaultExprs(map[string]string{"postgres": "0", "mysql": "0"})),
		field.Bytes("payload").SchemaType(map[string]string{"postgres": "bytea", "mysql": "longblob"})}
}
func (SigningKey) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "signing_keys"}}
}
