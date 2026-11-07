package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// User retains the compound physical identity (app_id,id). All adapter queries
// must bind both components; Ent's single-ID convenience APIs are not exposed.
type User struct{ ent.Schema }

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}), field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("status").MaxLen(16).SchemaType(map[string]string{"postgres": "character varying(16)", "mysql": "varchar(16)"}), field.Int64("session_epoch").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Int64("created_at").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}), field.Bytes("payload").SchemaType(map[string]string{"postgres": "bytea", "mysql": "longblob"}),
	}
}

func (User) Indexes() []ent.Index {
	return []ent.Index{index.Fields("app_id", "status", "id").StorageKey("users_status_id_idx")}
}
