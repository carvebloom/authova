package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Passkey struct{ ent.Schema }

func (Passkey) Fields() []ent.Field {
	return []ent.Field{field.String("id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("user_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("credential_hash").MaxLen(64).SchemaType(map[string]string{"postgres": "character varying(64)", "mysql": "varchar(64)"}),
		field.Int64("config_version").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Int64("created_at").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Int64("updated_at").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Bytes("payload").SchemaType(map[string]string{"postgres": "bytea", "mysql": "longblob"})}
}
func (Passkey) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "passkeys"}}
}

func (Passkey) Indexes() []ent.Index {
	return []ent.Index{index.Fields("app_id", "credential_hash").Unique().StorageKey("passkeys_app_id_credential_hash_key"), index.Fields("app_id", "user_id", "id").StorageKey("passkeys_user")}
}
