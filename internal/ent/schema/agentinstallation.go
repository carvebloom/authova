package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type AgentInstallation struct{ ent.Schema }

func (AgentInstallation) Fields() []ent.Field {
	return []ent.Field{field.String("id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("agent_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("tenant_id").MaxLen(128).SchemaType(map[string]string{"postgres": "character varying(128)", "mysql": "varchar(128)"}),
		field.String("user_id").MaxLen(36).Optional().Nillable().SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("status").MaxLen(16).SchemaType(map[string]string{"postgres": "character varying(16)", "mysql": "varchar(16)"}),
		field.Int64("config_version").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Int64("expires_at").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Bytes("payload").SchemaType(map[string]string{"postgres": "bytea", "mysql": "longblob"})}
}
func (AgentInstallation) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "agent_installations"}}
}

func (AgentInstallation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("app_id", "agent_id", "id").StorageKey("agent_installations_agent"), index.Fields("app_id", "tenant_id", "id").StorageKey("agent_installations_tenant"), index.Fields("app_id", "user_id", "id").StorageKey("agent_installations_user")}
}
