package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Session struct{ ent.Schema }

func (Session) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}), field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}), field.String("user_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}), field.String("handle_digest").MaxLen(64).SchemaType(map[string]string{"postgres": "character varying(64)", "mysql": "varchar(64)"}), field.String("status").MaxLen(16).SchemaType(map[string]string{"postgres": "character varying(16)", "mysql": "varchar(16)"}), field.Int64("session_epoch").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}), field.Int64("expires_at").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}), field.Bytes("payload").SchemaType(map[string]string{"postgres": "bytea", "mysql": "longblob"}),
	}
}

func (Session) Indexes() []ent.Index {
	return []ent.Index{index.Fields("app_id", "handle_digest").Unique().StorageKey("sessions_app_id_handle_digest_key"), index.Fields("app_id", "expires_at").StorageKey("sessions_expires_at_idx"), index.Fields("app_id", "user_id", "id").StorageKey("sessions_user_id_id_idx")}
}
