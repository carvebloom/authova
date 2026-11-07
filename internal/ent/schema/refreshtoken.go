package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type RefreshToken struct{ ent.Schema }

func (RefreshToken) Fields() []ent.Field {
	return []ent.Field{field.String("id").MaxLen(64).StorageKey("digest").SchemaType(map[string]string{"postgres": "character varying(64)", "mysql": "varchar(64)"}),
		field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("family_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.Int64("generation").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Int16("used").SchemaType(map[string]string{"postgres": "smallint", "mysql": "smallint"}),
		field.Int64("expires_at").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Bytes("payload").SchemaType(map[string]string{"postgres": "bytea", "mysql": "longblob"})}
}
func (RefreshToken) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "refresh_tokens"}}
}

func (RefreshToken) Indexes() []ent.Index {
	return []ent.Index{index.Fields("app_id", "family_id", "generation").Unique().StorageKey("refresh_tokens_app_id_family_id_generation_key"), index.Fields("app_id", "expires_at").StorageKey("refresh_tokens_expires_at_idx")}
}
