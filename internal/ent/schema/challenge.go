package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Challenge struct{ ent.Schema }

func (Challenge) Fields() []ent.Field {
	return []ent.Field{field.String("id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("flow_id").MaxLen(64).SchemaType(map[string]string{"postgres": "character varying(64)", "mysql": "varchar(64)"}),
		field.Int64("expires_at").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Bytes("payload").SchemaType(map[string]string{"postgres": "bytea", "mysql": "longblob"})}
}
func (Challenge) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "challenges"}}
}

func (Challenge) Indexes() []ent.Index {
	return []ent.Index{index.Fields("app_id", "expires_at").StorageKey("challenges_expires_at_idx")}
}
