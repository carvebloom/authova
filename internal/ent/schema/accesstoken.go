package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type AccessToken struct{ ent.Schema }

func (AccessToken) Fields() []ent.Field {
	return []ent.Field{field.String("id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("client_id").MaxLen(64).SchemaType(map[string]string{"postgres": "character varying(64)", "mysql": "varchar(64)"}),
		field.String("user_id").MaxLen(36).Optional().Nillable().SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("session_id").MaxLen(36).Optional().Nillable().SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("family_id").MaxLen(36).Optional().Nillable().SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.Int64("expires_at").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Int16("revoked").SchemaType(map[string]string{"postgres": "smallint", "mysql": "smallint"}),
		field.Bytes("payload").SchemaType(map[string]string{"postgres": "bytea", "mysql": "longblob"})}
}
func (AccessToken) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "access_tokens"}}
}

func (AccessToken) Indexes() []ent.Index {
	return []ent.Index{index.Fields("app_id", "expires_at").StorageKey("access_tokens_expires_at_idx")}
}
