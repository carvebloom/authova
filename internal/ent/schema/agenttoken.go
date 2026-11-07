package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type AgentToken struct{ ent.Schema }

func (AgentToken) Fields() []ent.Field {
	return []ent.Field{field.String("id").StorageKey("token_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("agent_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("installation_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("grant_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("tenant_id").MaxLen(128).SchemaType(map[string]string{"postgres": "character varying(128)", "mysql": "varchar(128)"}),
		field.String("mode").MaxLen(16).SchemaType(map[string]string{"postgres": "character varying(16)", "mysql": "varchar(16)"}),
		field.String("user_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.Int64("agent_version").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Int64("installation_version").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Int64("grant_version").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"})}
}
func (AgentToken) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "agent_tokens"}}
}

func (AgentToken) Indexes() []ent.Index {
	return []ent.Index{index.Fields("app_id", "grant_id", "id").StorageKey("agent_tokens_grant")}
}
