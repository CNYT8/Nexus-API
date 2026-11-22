package model

import (
	"fmt"
	"strings"

	"gorm.io/gorm/schema"
)

// This is the immutable nexus-v1 import contract, independent of live models.
// New model fields must never become required archive columns or copied values.
func compatibilityV1Schema(table string) (*schema.Schema, error) {
	var integers, text, booleans string
	primary := "id"
	switch table {
	case "nexus_user_sessions":
		integers = "id,user_id,auth_version,created_at,expires_at,revoked_at,verified_at"
		text = "secret_hash,verified_method,login_method,ip,user_agent"
	case "nexus_auth_flows":
		integers = "id,user_id,auth_version,expires_at,consumed_at"
		text = "secret_hash,purpose,provider,binding_hash,payload"
	case "nexus_user_access_tokens":
		integers = "id,user_id,auth_version,created_at,expires_at,revoked_at"
		text = "name,secret_hash,prefix,scopes"
	case "nexus_task_plugins":
		integers = "id,created_at"
		text = "name,version,schema,expression"
		booleans = "retain_result"
	case "nexus_system_tasks":
		integers = "id,user_id,target,processed,created_at,updated_at,progress_version"
		text = "kind,status,error,log_backend"
		booleans = "progress_exact"
	case "nexus_schema_compatibility":
		primary = "source"
		integers = "copied_rows,completed_at"
		text = "source,destination,layout,disposition"
	default:
		return nil, fmt.Errorf("no frozen compatibility contract for %s", table)
	}
	s := &schema.Schema{Table: table, FieldsByDBName: make(map[string]*schema.Field)}
	for _, group := range []struct {
		names string
		kind  schema.DataType
	}{{integers, schema.Int}, {text, schema.String}, {booleans, schema.Bool}} {
		for _, name := range strings.Split(group.names, ",") {
			if name == "" {
				continue
			}
			field := &schema.Field{DBName: name, DataType: group.kind, PrimaryKey: name == primary}
			s.Fields = append(s.Fields, field)
			s.FieldsByDBName[name] = field
			if field.PrimaryKey {
				s.PrimaryFields = append(s.PrimaryFields, field)
			}
		}
	}
	return s, nil
}
