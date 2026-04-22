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
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "title", Type: "text", Required: true},
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



func TestValidate_UnderscorePrefixTable(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["_internal"] = domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
		},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables._internal")
}

func TestValidate_NoPrimaryKey(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["todos"] = domain.Table{
		Fields: []domain.Field{
			{Name: "title", Type: "text", Required: true},
		},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos")
}

func TestValidate_NoFields(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["empty"] = domain.Table{
		Fields: []domain.Field{},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.empty")
}

func TestValidate_MissingFieldType(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.Fields = append(table.Fields, domain.Field{Name: "bad"})
	cfg.Tables["todos"] = table

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.fields.bad")
}

func TestValidate_UnknownFieldType(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.Fields = append(table.Fields, domain.Field{Name: "bad", Type: "unknowntype"})
	cfg.Tables["todos"] = table

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.fields.bad.type")
}

func TestValidate_FKMissingReferences(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.Fields = append(table.Fields, domain.Field{
		Name: "user_id", ForeignKey: &domain.ForeignKey{References: ""},
	})
	cfg.Tables["todos"] = table

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.fields.user_id.foreign_key.references")
}

func TestValidate_FKInvalidFormat(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.Fields = append(table.Fields, domain.Field{
		Name: "user_id", ForeignKey: &domain.ForeignKey{References: "noDot"},
	})
	cfg.Tables["todos"] = table

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.fields.user_id.foreign_key.references")
}

func TestValidate_FKInvalidOnDelete(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.Fields = append(table.Fields, domain.Field{
		Name: "user_id", ForeignKey: &domain.ForeignKey{References: "users.id", OnDelete: "explode"},
	})
	cfg.Tables["todos"] = table

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.fields.user_id.foreign_key.on_delete")
}

func TestValidate_FKReferencesNonexistentTable(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.Fields = append(table.Fields, domain.Field{
		Name: "cat_id", ForeignKey: &domain.ForeignKey{References: "categories.id"},
	})
	cfg.Tables["todos"] = table

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.fields.cat_id.foreign_key.references")
}

func TestValidate_FKReferencesUsersOK(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.Fields = append(table.Fields, domain.Field{
		Name: "user_id", ForeignKey: &domain.ForeignKey{References: "users.id", OnDelete: "cascade"},
	})
	cfg.Tables["todos"] = table

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
	table := cfg.Tables["todos"]
	table.Fields = append(table.Fields, domain.Field{
		Name: "priority", Type: "integer", Enum: []string{"low", "high"},
	})
	cfg.Tables["todos"] = table

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.fields.priority.enum")
}

func TestValidate_InvalidDefault(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.Fields = append(table.Fields, domain.Field{
		Name: "bad", Type: "text", Default: "random_func()",
	})
	cfg.Tables["todos"] = table

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
			table := cfg.Tables["todos"]
			table.Fields = append(table.Fields, domain.Field{
				Name: "col", Type: "text", Default: tt.val,
			})
			cfg.Tables["todos"] = table
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

func TestValidate_FunctionMissingBody(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Functions = map[string]domain.Function{
		"bad": {
			Language:   "plpgsql",
			Volatility: "volatile",
			Security:   "invoker",
			Returns:    domain.FuncReturn{Type: "void"},
		},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "functions.bad.body")
}

func TestValidate_DataUnknownTable(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Data = map[string]map[string]string{
		"nonexistent": {
			"init": "./seeds/nonexistent.csv",
		},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "data.nonexistent")
}

func TestValidate_DataUsersOK(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Data = map[string]map[string]string{
		"users": {
			"demo": "./seeds/users.csv",
		},
	}

	errs := Validate(cfg)
	if errs != nil {
		t.Errorf("expected no errors for data.users, got: %v", errs)
	}
}

func TestValidate_DataEmptySource(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Data = map[string]map[string]string{
		"users": {
			"demo": "",
		},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "data.users.demo")
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
		},
		Tables: map[string]domain.Table{
			"users": {
				Fields: []domain.Field{
					{Name: "display_name", Type: "text", Required: true},
					{Name: "avatar_url", Type: "text"},
				},
			},
			"teams": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "name", Type: "text", Required: true},
					{Name: "slug", Type: "varchar(63)", Required: true},
					{Name: "created_at", Type: "timestamptz", Required: true, Default: "now()"},
				},
				Indexes: []domain.Index{
					{Columns: []string{"slug"}, Unique: true},
				},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select"}, Check: "true"},
				},
			},
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "team_id", ForeignKey: &domain.ForeignKey{References: "teams.id", OnDelete: "cascade"}},
					{Name: "user_id", ForeignKey: &domain.ForeignKey{References: "users.id", OnDelete: "cascade"}},
					{Name: "title", Type: "text", Required: true},
					{Name: "status", Type: "text", Required: true, Enum: []string{"pending", "active", "done"}, Default: "pending"},
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
		Data: map[string]map[string]string{
			"users": {"demo": "./seeds/users.csv"},
			"teams": {"init": "./seeds/teams.csv"},
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

// --- RPC Functions validation ---

func validRPCFunction() domain.Function {
	return domain.Function{
		Language:   "plpgsql",
		Volatility: "stable",
		Security:   "invoker",
		Returns:    domain.FuncReturn{Type: "int"},
		Body:       "BEGIN RETURN 1; END;",
		Args: []domain.FuncArg{
			{Name: "x", Type: "int"},
		},
	}
}

func TestValidate_RPCFunction_Valid(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Functions = map[string]domain.Function{
		"add_one": validRPCFunction(),
	}
	if errs := Validate(cfg); errs != nil {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidate_RPCFunction_BadName(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Functions = map[string]domain.Function{
		"drop table users;--": validRPCFunction(),
	}
	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected error for malicious function name")
	}
	assertHasErrorAt(t, errs, "functions.drop table users;--")
}

func TestValidate_RPCFunction_BadArgName(t *testing.T) {
	cfg := validBaseConfig()
	fn := validRPCFunction()
	fn.Args = []domain.FuncArg{{Name: "x); DROP TABLE users; --", Type: "int"}}
	cfg.Functions = map[string]domain.Function{"f": fn}
	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected error for malicious arg name")
	}
	assertHasErrorAt(t, errs, "functions.f.args[0].name")
}

func TestValidate_RPCFunction_BadArgType(t *testing.T) {
	cfg := validBaseConfig()
	fn := validRPCFunction()
	fn.Args = []domain.FuncArg{{Name: "x", Type: "int; DROP TABLE users"}}
	cfg.Functions = map[string]domain.Function{"f": fn}
	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected error for malicious arg type")
	}
	assertHasErrorAt(t, errs, "functions.f.args[0].type")
}

func TestValidate_RPCFunction_RejectsReservedDollarTag(t *testing.T) {
	cfg := validBaseConfig()
	fn := validRPCFunction()
	fn.Body = "BEGIN RAISE 'oops $ub$ ok'; END;"
	cfg.Functions = map[string]domain.Function{"f": fn}
	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected error for body containing $ub$ tag")
	}
	assertHasErrorAt(t, errs, "functions.f.body")
}

func TestValidate_RPCFunction_UnknownLanguage(t *testing.T) {
	cfg := validBaseConfig()
	fn := validRPCFunction()
	fn.Language = "plpython"
	cfg.Functions = map[string]domain.Function{"f": fn}
	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected error for unsupported language")
	}
	assertHasErrorAt(t, errs, "functions.f.language")
}

func TestValidate_RPCFunction_DuplicateArg(t *testing.T) {
	cfg := validBaseConfig()
	fn := validRPCFunction()
	fn.Args = []domain.FuncArg{
		{Name: "x", Type: "int"},
		{Name: "x", Type: "text"},
	}
	cfg.Functions = map[string]domain.Function{"f": fn}
	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected error for duplicate arg name")
	}
	assertHasErrorAt(t, errs, "functions.f.args[1].name")
}
