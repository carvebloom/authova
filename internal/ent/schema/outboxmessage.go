package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type OutboxMessage struct{ ent.Schema }

func (OutboxMessage) Fields() []ent.Field {
	return []ent.Field{
		field.String("delivery_state").MaxLen(16).SchemaType(map[string]string{"postgres": "character varying(16)", "mysql": "varchar(16)"}).Default("unknown"),
		field.String("failure_phase").MaxLen(16).SchemaType(map[string]string{"postgres": "character varying(16)", "mysql": "varchar(16)"}).Default("unknown"), field.String("failure_code").MaxLen(32).SchemaType(map[string]string{"postgres": "character varying(32)", "mysql": "varchar(32)"}).Default("unknown"), field.Int16("outcome_unknown").Default(0),
		field.String("id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}), field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}), field.Int64("available_at").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}), field.Int("attempts").SchemaType(map[string]string{"postgres": "integer", "mysql": "int"}), field.Int16("completed").SchemaType(map[string]string{"postgres": "smallint", "mysql": "smallint"}), field.Bytes("payload").SchemaType(map[string]string{"postgres": "bytea", "mysql": "longblob"}),
	}
}
func (OutboxMessage) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "outbox"}}
}

func (OutboxMessage) Indexes() []ent.Index {
	return []ent.Index{index.Fields("app_id", "completed", "available_at", "id").StorageKey("outbox_app_ready"), index.Fields("completed", "available_at", "id").StorageKey("outbox_ready_idx")}
}
