package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type ExternalIdentity struct{ ent.Schema }

func (ExternalIdentity) Fields() []ent.Field {
	return []ent.Field{field.String("id").StorageKey("subject_digest").MaxLen(64).SchemaType(map[string]string{"postgres": "character varying(64)", "mysql": "varchar(64)"}),
		field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("connection_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}),
		field.String("user_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"})}
}
func (ExternalIdentity) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "external_identities"}}
}

func (ExternalIdentity) Indexes() []ent.Index {
	return []ent.Index{index.Fields("app_id", "user_id", "connection_id").StorageKey("external_identities_user_lookup")}
}
