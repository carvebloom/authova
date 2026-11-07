package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type PasskeyCeremony struct{ ent.Schema }

func (PasskeyCeremony) Fields() []ent.Field {
	return []ent.Field{field.String("id").MaxLen(64).SchemaType(map[string]string{"postgres": "character varying(64)", "mysql": "varchar(64)"}),
		field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("user_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.Int64("expires_at").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Bytes("payload").SchemaType(map[string]string{"postgres": "bytea", "mysql": "longblob"})}
}
func (PasskeyCeremony) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "passkey_ceremonies"}}
}

func (PasskeyCeremony) Indexes() []ent.Index {
	return []ent.Index{index.Fields("expires_at").StorageKey("passkey_ceremonies_expiry")}
}
