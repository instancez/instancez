package config

import (
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
)

func validBaseConfig() *domain.Config {
	return &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "test"},
		Tables: map[string]domain.Table{
			"todos": {
				Fields: map[string]domain.Field{
					"id":    {Type: "bigserial", PrimaryKey: true},
					"title": {Type: "text", Required: true},
				},
			},
		},
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg := validBaseConfig()
	errs := Validate(cfg)
	if errs != nil {
		t.Errorf("expected no errors, got %d: %v", len(errs), errs)
	}
}

func TestValidate_WrongVersion(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Version = 0

	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected errors")
	}
	assertHasErrorAt(t, errs, "version")
}

func TestValidate_ReservedTableName(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["users"] = domain.Table{
		Fields: map[string]domain.Field{
			"id": {Type: "bigserial", PrimaryKey: true},
		},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.users")
}

func TestValidate_UnderscorePrefixTable(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["_internal"] = domain.Table{
		Fields: map[string]domain.Field{
			"id": {Type: "bigserial", PrimaryKey: true},
		},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables._internal")
}

func TestValidate_NoPrimaryKey(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["todos"] = domain.Table{
		Fields: map[string]domain.Field{
			"title": {Type: "text", Required: true},
		},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos")
}

func TestValidate_NoFields(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["empty"] = domain.Table{
		Fields: map[string]domain.Field{},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.empty")
}

func TestValidate_MissingFieldType(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["todos"].Fields["bad"] = domain.Field{}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.fields.bad")
}

func TestValidate_UnknownFieldType(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["todos"].Fields["bad"] = domain.Field{Type: "unknowntype"}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.fields.bad.type")
}

func TestValidate_FKMissingReferences(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["todos"].Fields["user_id"] = domain.Field{
		ForeignKey: &domain.ForeignKey{References: ""},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.fields.user_id.foreign_key.references")
}

func TestValidate_FKInvalidFormat(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["todos"].Fields["user_id"] = domain.Field{
		ForeignKey: &domain.ForeignKey{References: "noDot"},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.fields.user_id.foreign_key.references")
}

func TestValidate_FKInvalidOnDelete(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["todos"].Fields["user_id"] = domain.Field{
		ForeignKey: &domain.ForeignKey{References: "users.id", OnDelete: "explode"},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.fields.user_id.foreign_key.on_delete")
}

func TestValidate_FKReferencesNonexistentTable(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["todos"].Fields["cat_id"] = domain.Field{
		ForeignKey: &domain.ForeignKey{References: "categories.id"},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.fields.cat_id.foreign_key.references")
}

func TestValidate_FKReferencesUsersOK(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["todos"].Fields["user_id"] = domain.Field{
		ForeignKey: &domain.ForeignKey{References: "users.id", OnDelete: "cascade"},
	}

	errs := Validate(cfg)
	if errs != nil {
		t.Errorf("expected no errors for FK to users.id, got: %v", errs)
	}
}

func TestValidate_IndexUnknownColumn(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.Indexes = []domain.Index{{Columns: []string{"nonexistent"}}}
	cfg.Tables["todos"] = table

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.indexes[0]")
}

func TestValidate_RLSMissingCheck(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.RLS = []domain.RLSPolicy{{Operations: []string{"select"}, Check: ""}}
	cfg.Tables["todos"] = table

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.rls[0].check")
}

func TestValidate_RLSInvalidOp(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.RLS = []domain.RLSPolicy{{Operations: []string{"truncate"}, Check: "true"}}
	cfg.Tables["todos"] = table

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.rls[0].operations")
}

func TestValidate_SearchableUnknownColumn(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.Searchable = []string{"nonexistent"}
	cfg.Tables["todos"] = table

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.searchable")
}

func TestValidate_EnumOnNonStringType(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["todos"].Fields["priority"] = domain.Field{
		Type: "integer",
		Enum: []string{"low", "high"},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.fields.priority.enum")
}

func TestValidate_InvalidDefault(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["todos"].Fields["bad"] = domain.Field{
		Type:    "text",
		Default: "random_func()",
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.fields.bad.default")
}

func TestValidate_ValidDefaults(t *testing.T) {
	tests := []struct {
		name string
		val  any
	}{
		{"string literal", "hello"},
		{"integer literal", 42},
		{"bool literal", false},
		{"now()", "now()"},
		{"uuid_v7()", "uuid_v7()"},
		{"current_date", "current_date"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBaseConfig()
			cfg.Tables["todos"].Fields["col"] = domain.Field{
				Type:    "text",
				Default: tt.val,
			}
			errs := Validate(cfg)
			if errs != nil {
				t.Errorf("expected no errors for default %v, got: %v", tt.val, errs)
			}
		})
	}
}

func TestValidate_Providers(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Providers.Email = &domain.EmailProvider{Type: "mailgun"}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "providers.email.type")
}

func TestValidate_TriggerNoAction(t *testing.T) {
	cfg := validBaseConfig()
	cfg.On = map[string]domain.Trigger{
		"bad": {Events: []string{"todos.insert"}},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "on.bad")
}

func TestValidate_TriggerNoTrigger(t *testing.T) {
	cfg := validBaseConfig()
	cfg.On = map[string]domain.Trigger{
		"bad": {Webhook: &domain.WebhookAction{URL: "https://example.com"}},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "on.bad")
}

func TestValidate_TriggerBothEventsAndSchedule(t *testing.T) {
	cfg := validBaseConfig()
	cfg.On = map[string]domain.Trigger{
		"bad": {
			Events:   []string{"todos.insert"},
			Schedule: "0 9 * * *",
			Webhook:  &domain.WebhookAction{URL: "https://example.com"},
		},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "on.bad")
}

func TestValidate_TriggerInvalidEventPattern(t *testing.T) {
	cfg := validBaseConfig()
	cfg.On = map[string]domain.Trigger{
		"bad": {
			Events:  []string{"invalid"},
			Webhook: &domain.WebhookAction{URL: "https://example.com"},
		},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "on.bad.events")
}

func TestValidate_TriggerUnknownTable(t *testing.T) {
	cfg := validBaseConfig()
	cfg.On = map[string]domain.Trigger{
		"bad": {
			Events:  []string{"nonexistent.insert"},
			Webhook: &domain.WebhookAction{URL: "https://example.com"},
		},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "on.bad.events")
}

func TestValidate_TriggerWildcardTableOK(t *testing.T) {
	cfg := validBaseConfig()
	cfg.On = map[string]domain.Trigger{
		"audit": {
			Events:  []string{"*.delete"},
			Webhook: &domain.WebhookAction{URL: "https://example.com"},
		},
	}

	errs := Validate(cfg)
	if errs != nil {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidate_FunctionParamMismatch(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Functions = map[string]domain.Function{
		"bad": {
			Query:   "SELECT * FROM todos WHERE id = $1 AND user_id = $2",
			Params:  map[string]domain.FuncParam{"id": {Type: "integer"}},
			Returns: domain.FuncReturn{Type: "rows"},
		},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "functions.bad")
}

func TestValidate_SeedUnknownTable(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Seeds = map[string][]map[string]any{
		"nonexistent": {{
			"id": 1,
		}},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "seeds.nonexistent")
}

func TestValidate_SeedUsersOK(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Seeds = map[string][]map[string]any{
		"users": {{
			"email":    "admin@test.com",
			"password": "secret",
		}},
	}

	errs := Validate(cfg)
	if errs != nil {
		t.Errorf("expected no errors for seeds.users, got: %v", errs)
	}
}

func TestValidate_StorageInvalidSize(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Storage = map[string]domain.Bucket{
		"avatars": {MaxSize: "invalid"},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "storage.avatars.max_size")
}

func TestValidate_StorageValidSize(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Storage = map[string]domain.Bucket{
		"avatars": {MaxSize: "2MB"},
	}

	errs := Validate(cfg)
	if errs != nil {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidate_FullExampleConfig(t *testing.T) {
	// Test with a config resembling the worked example
	cfg := &domain.Config{
		Version:    1,
		Project:    domain.Project{Name: "Acme Todo App"},
		Extensions: []string{"pgcrypto", "pg_trgm"},
		Providers: domain.Providers{
			Email:   &domain.EmailProvider{Type: "resend"},
			Storage: &domain.StorageProvider{Type: "s3"},
		},
		Auth: &domain.Auth{
			JWTExpiry:     "15m",
			RefreshTokens: true,
			Email:         &domain.AuthEmail{VerifyEmail: true},
			Fields: map[string]domain.Field{
				"display_name": {Type: "text", Required: true},
				"avatar_url":   {Type: "text"},
			},
		},
		Tables: map[string]domain.Table{
			"teams": {
				Fields: map[string]domain.Field{
					"id":         {Type: "bigserial", PrimaryKey: true},
					"name":       {Type: "text", Required: true},
					"slug":       {Type: "varchar(63)", Required: true},
					"created_at": {Type: "timestamptz", Required: true, Default: "now()"},
				},
				Indexes: []domain.Index{
					{Columns: []string{"slug"}, Unique: true},
				},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select"}, Check: "true"},
				},
			},
			"todos": {
				Fields: map[string]domain.Field{
					"id":      {Type: "bigserial", PrimaryKey: true},
					"team_id": {ForeignKey: &domain.ForeignKey{References: "teams.id", OnDelete: "cascade"}},
					"user_id": {ForeignKey: &domain.ForeignKey{References: "users.id", OnDelete: "cascade"}},
					"title":   {Type: "text", Required: true},
					"status":  {Type: "text", Required: true, Enum: []string{"pending", "active", "done"}, Default: "pending"},
				},
				Searchable: []string{"title"},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select"}, Check: "user_id = auth.uid()"},
				},
			},
		},
		Storage: map[string]domain.Bucket{
			"avatars": {
				MaxSize: "2MB",
				Types:   []string{"image/*"},
				Public:  true,
				RLS: []domain.RLSPolicy{
					{Operations: []string{"insert", "delete"}, Check: "uploaded_by = auth.uid()"},
				},
			},
		},
		On: map[string]domain.Trigger{
			"welcome": {
				Events: []string{"users.insert"},
				Email: &domain.EmailAction{
					To:      "{{data.email}}",
					Subject: "Welcome!",
					Body:    "Hello!",
				},
			},
		},
		Seeds: map[string][]map[string]any{
			"users": {{"email": "admin@test.com", "password": "secret"}},
			"teams": {{"id": 1, "name": "Acme", "slug": "acme"}},
		},
	}

	errs := Validate(cfg)
	if errs != nil {
		for _, e := range errs {
			t.Errorf("  %s: %s", e.Path, e.Message)
		}
		t.Fatalf("expected no errors, got %d", len(errs))
	}
}

// assertHasErrorAt checks that at least one error has the given path prefix.
func assertHasErrorAt(t *testing.T, errs domain.ValidationErrors, pathPrefix string) {
	t.Helper()
	if errs == nil {
		t.Fatalf("expected errors at %q, got none", pathPrefix)
	}
	for _, e := range errs {
		if len(e.Path) >= len(pathPrefix) && e.Path[:len(pathPrefix)] == pathPrefix {
			return
		}
	}
	paths := make([]string, len(errs))
	for i, e := range errs {
		paths[i] = e.Path
	}
	t.Errorf("expected error at %q, got errors at: %v", pathPrefix, paths)
}
