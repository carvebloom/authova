package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type OAuthClient struct{ ent.Schema }

func (OAuthClient) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}), field.String("app_id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}), field.String("client_id").MaxLen(64).SchemaType(map[string]string{"postgres": "character varying(64)", "mysql": "varchar(64)"}),
		field.String("status").MaxLen(16).SchemaType(map[string]string{"postgres": "character varying(16)", "mysql": "varchar(16)"}), field.Bytes("payload").SchemaType(map[string]string{"postgres": "bytea", "mysql": "longblob"}), field.String("kind").SchemaType(map[string]string{"postgres": "character varying(16)", "mysql": "varchar(16)"}).Annotations(entsql.DefaultExprs(map[string]string{"postgres": "'public'", "mysql": "\"public\""})), field.Int("introspection_count").SchemaType(map[string]string{"postgres": "integer", "mysql": "int"}).Annotations(entsql.DefaultExprs(map[string]string{"postgres": "0", "mysql": "0"})),
	}
}

func (OAuthClient) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "clients"}}
}

func (OAuthClient) Indexes() []ent.Index {
	return []ent.Index{index.Fields("app_id", "client_id").Unique().StorageKey("clients_app_id_client_id_key")}
}
