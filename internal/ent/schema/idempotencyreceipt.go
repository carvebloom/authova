package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type IdempotencyReceipt struct{ ent.Schema }

func (IdempotencyReceipt) Fields() []ent.Field {
	return []ent.Field{field.String("id").StorageKey("idem_key").MaxLen(128).SchemaType(map[string]string{"postgres": "character varying(128)", "mysql": "varchar(128)"}),
		field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("fingerprint").MaxLen(64).SchemaType(map[string]string{"postgres": "character varying(64)", "mysql": "varchar(64)"}),
		field.Int64("expires_at").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Bytes("payload").SchemaType(map[string]string{"postgres": "bytea", "mysql": "longblob"})}
}
func (IdempotencyReceipt) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "idempotency"}}
}
