package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// App maps the v1 baseline table; encrypted domain payloads in other tables are unchanged.
type App struct{ ent.Schema }

func (App) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").MaxLen(36).SchemaType(map[string]string{"postgres": "character varying(36)", "mysql": "varchar(36)"}), field.String("app_key").MaxLen(48).SchemaType(map[string]string{"postgres": "character varying(48)", "mysql": "varchar(48)"}),
		field.String("slug").MaxLen(63).SchemaType(map[string]string{"postgres": "character varying(63)", "mysql": "varchar(63)"}), field.String("name").MaxLen(200).SchemaType(map[string]string{"postgres": "character varying(200)", "mysql": "varchar(200)"}),
		field.String("status").MaxLen(16).SchemaType(map[string]string{"postgres": "character varying(16)", "mysql": "varchar(16)"}), field.Int64("config_version").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}),
		field.Int64("created_at").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}), field.Int16("require_mfa").SchemaType(map[string]string{"postgres": "smallint", "mysql": "smallint"}).Annotations(entsql.DefaultExprs(map[string]string{"postgres": "0", "mysql": "0"})), field.String("issuer").SchemaType(map[string]string{"postgres": "character varying(512)", "mysql": "varchar(512)"}).Annotations(entsql.DefaultExprs(map[string]string{"postgres": "''", "mysql": "\"\""})),
		field.String("authentication_policy").SchemaType(map[string]string{"postgres": "text", "mysql": "varchar(1024)"}).Annotations(entsql.DefaultExprs(map[string]string{"postgres": "'{\"registration_enabled\":true,\"password_login_enabled\":true,\"email_otp_enabled\":true,\"phone_otp_enabled\":true,\"federation_enabled\":true,\"external_registration_enabled\":true,\"passkey_enabled\":false}'", "mysql": "\"{\\\"registration_enabled\\\":true,\\\"password_login_enabled\\\":true,\\\"email_otp_enabled\\\":true,\\\"phone_otp_enabled\\\":true,\\\"federation_enabled\\\":true,\\\"external_registration_enabled\\\":true,\\\"passkey_enabled\\\":false}\""})), field.Int64("key_epoch").SchemaType(map[string]string{"postgres": "bigint", "mysql": "bigint"}).Annotations(entsql.DefaultExprs(map[string]string{"postgres": "1", "mysql": "1"})), field.String("resource_limits").SchemaType(map[string]string{"postgres": "text", "mysql": "varchar(1024)"}).Annotations(entsql.DefaultExprs(map[string]string{"postgres": "'{\"http_concurrency\":256,\"kdf_concurrency\":2,\"db_concurrency\":32,\"requests_per_second\":20000,\"email_challenges_per_hour\":100000,\"sms_challenges_per_hour\":10000}'", "mysql": "\"{\\\"http_concurrency\\\":256,\\\"kdf_concurrency\\\":2,\\\"db_concurrency\\\":32,\\\"requests_per_second\\\":20000,\\\"email_challenges_per_hour\\\":100000,\\\"sms_challenges_per_hour\\\":10000}\""})),
	}
}

func (App) Indexes() []ent.Index {
	return []ent.Index{index.Fields("app_key").Unique().StorageKey("apps_app_key_key"), index.Fields("slug").Unique().StorageKey("apps_slug_key")}
}
