package config

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
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

func TestValidate_FunctionMissingBody(t *testing.T) {
	cfg := validBaseConfig()
	cfg.RPC = map[string]domain.Function{
		"bad": {
			Language:   "plpgsql",
			Volatility: "volatile",
			Security:   "invoker",
			Returns:    domain.FuncReturn{Type: "void"},
		},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "rpc.bad.body")
}

func TestValidate_DataUnknownTable(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Data = map[string]domain.TableData{
		"nonexistent": {CSVFiles: map[string]string{"init": "./seeds/nonexistent.csv"}},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "data.nonexistent")
}

func TestValidate_DataAuthUsersOK(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Auth = &domain.Auth{}
	cfg.Data = map[string]domain.TableData{
		"auth.users": {Rows: []map[string]any{{"email": "a@example.com", "password": "secret-pass"}}},
	}

	errs := Validate(cfg)
	if errs != nil {
		t.Errorf("expected no errors for data.auth.users with auth configured, got: %v", errs)
	}
}

func TestValidate_DataAuthUsersRequiresAuth(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Data = map[string]domain.TableData{
		"auth.users": {Rows: []map[string]any{{"email": "a@example.com"}}},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "data.auth.users")
}

func TestValidate_DataEmptySource(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Data = map[string]domain.TableData{
		"todos": {CSVFiles: map[string]string{"demo": ""}},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "data.todos.demo")
}

func TestValidate_StorageInvalidSize(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Providers.Storage = &domain.StorageProvider{Type: "local"}
	cfg.Storage = map[string]domain.Bucket{
		"avatars": {MaxSize: "invalid"},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "storage.avatars.max_size")
}

func TestValidate_StorageValidSize(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Providers.Storage = &domain.StorageProvider{Type: "local"}
	cfg.Storage = map[string]domain.Bucket{
		"avatars": {MaxSize: "2MB"},
	}

	errs := Validate(cfg)
	if errs != nil {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

// TestValidate_StorageBucketsRequireProvider pins that declaring storage
// buckets without a providers.storage backend is rejected. Without this guard,
// `dev` and `validate` would accept the config and only `serve` would fail at
// boot (and dev would later nil-panic on the first file operation).
func TestValidate_StorageBucketsRequireProvider(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Storage = map[string]domain.Bucket{
		"avatars": {MaxSize: "2MB"},
	}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "providers.storage")
}

// TestValidate_VerifyEmailRequiresProvider pins that enabling
// auth.email.verify_email without a providers.email backend is rejected, so
// signups can't be left permanently unconfirmable.
func TestValidate_VerifyEmailRequiresProvider(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Auth = &domain.Auth{Email: &domain.AuthEmail{VerifyEmail: true}}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "providers.email")
}

// TestValidate_VerifyEmailWithProviderOK confirms the verify_email guard is
// satisfied once an email provider is present.
func TestValidate_VerifyEmailWithProviderOK(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Providers.Email = &domain.EmailProvider{Type: "resend", APIKey: "re_x"}
	cfg.Auth = &domain.Auth{Email: &domain.AuthEmail{VerifyEmail: true}}

	errs := Validate(cfg)
	for _, e := range errs {
		if e.Path == "providers.email" {
			t.Fatalf("did not expect providers.email error, got: %s", e.Message)
		}
	}
}

func TestValidate_FullExampleConfig(t *testing.T) {
	// Test with a config resembling the worked example
	cfg := &domain.Config{
		Version:    1,
		Project:    domain.Project{Name: "Acme Todo App"},
		Extensions: []string{"pgcrypto", "pg_trgm"},
		Providers: domain.Providers{
			Email:   &domain.EmailProvider{Type: "resend", APIKey: "${INSTANCEZ_RESEND_API_KEY}"},
			Storage: &domain.StorageProvider{Type: "s3", Bucket: "${INSTANCEZ_S3_BUCKET}"},
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
		Data: map[string]domain.TableData{
			"users": {CSVFiles: map[string]string{"demo": "./seeds/users.csv"}},
			"teams": {CSVFiles: map[string]string{"init": "./seeds/teams.csv"}},
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

// TestValidate_UnknownEmailTemplateRejected pins the supported template
// kinds: verification, magiclink, reset. Anything else (including the old
// dashboard's "verify" key, which the backend never read) is an error.
func TestValidate_UnknownEmailTemplateRejected(t *testing.T) {
	withTemplates := func(templates map[string]domain.EmailTemplate) *domain.Config {
		cfg := validBaseConfig()
		cfg.Auth = &domain.Auth{
			JWTExpiry:     "15m",
			RefreshTokens: true,
			Email:         &domain.AuthEmail{VerifyEmail: true, Templates: templates},
		}
		return cfg
	}

	errs := Validate(withTemplates(map[string]domain.EmailTemplate{
		"verify": {Subject: "s", Body: "b"},
	}))
	assertHasErrorAt(t, errs, "auth.email.templates.verify")

	errs = Validate(withTemplates(map[string]domain.EmailTemplate{
		"verification": {Subject: "s", Body: "b"},
		"magiclink":    {Subject: "s", Body: "b"},
		"reset":        {Subject: "s", Body: "b"},
	}))
	for _, e := range errs {
		if strings.HasPrefix(e.Path, "auth.email.templates") {
			t.Fatalf("expected supported template names to validate, got %s: %s", e.Path, e.Message)
		}
	}
}

// TestValidate_RemovedProvidersRejected pins the removal of the gcs/minio
// storage providers and the sendgrid email provider.
func TestValidate_RemovedProvidersRejected(t *testing.T) {
	for _, storageType := range []string{"minio", "gcs"} {
		cfg := validBaseConfig()
		cfg.Providers.Storage = &domain.StorageProvider{Type: storageType, Bucket: "b"}
		errs := Validate(cfg)
		assertHasErrorAt(t, errs, "providers.storage.type")
	}

	cfg := validBaseConfig()
	cfg.Providers.Email = &domain.EmailProvider{Type: "sendgrid", APIKey: "SG.x"}
	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "providers.email.type")
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
	cfg.RPC = map[string]domain.Function{
		"add_one": validRPCFunction(),
	}
	if errs := Validate(cfg); errs != nil {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidate_RPCFunction_BadName(t *testing.T) {
	cfg := validBaseConfig()
	cfg.RPC = map[string]domain.Function{
		"drop table users;--": validRPCFunction(),
	}
	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected error for malicious function name")
	}
	assertHasErrorAt(t, errs, "rpc.drop table users;--")
}

func TestValidate_RPCFunction_BadArgName(t *testing.T) {
	cfg := validBaseConfig()
	fn := validRPCFunction()
	fn.Args = []domain.FuncArg{{Name: "x); DROP TABLE users; --", Type: "int"}}
	cfg.RPC = map[string]domain.Function{"f": fn}
	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected error for malicious arg name")
	}
	assertHasErrorAt(t, errs, "rpc.f.args[0].name")
}

func TestValidate_RPCFunction_BadArgType(t *testing.T) {
	cfg := validBaseConfig()
	fn := validRPCFunction()
	fn.Args = []domain.FuncArg{{Name: "x", Type: "int; DROP TABLE users"}}
	cfg.RPC = map[string]domain.Function{"f": fn}
	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected error for malicious arg type")
	}
	assertHasErrorAt(t, errs, "rpc.f.args[0].type")
}

func TestValidate_RPCFunction_RejectsReservedDollarTag(t *testing.T) {
	cfg := validBaseConfig()
	fn := validRPCFunction()
	fn.Body = "BEGIN RAISE 'oops $ub$ ok'; END;"
	cfg.RPC = map[string]domain.Function{"f": fn}
	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected error for body containing $ub$ tag")
	}
	assertHasErrorAt(t, errs, "rpc.f.body")
}

func TestValidate_RPCFunction_UnknownLanguage(t *testing.T) {
	cfg := validBaseConfig()
	fn := validRPCFunction()
	fn.Language = "plpython"
	cfg.RPC = map[string]domain.Function{"f": fn}
	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected error for unsupported language")
	}
	assertHasErrorAt(t, errs, "rpc.f.language")
}

func TestValidate_RPCFunction_DuplicateArg(t *testing.T) {
	cfg := validBaseConfig()
	fn := validRPCFunction()
	fn.Args = []domain.FuncArg{
		{Name: "x", Type: "int"},
		{Name: "x", Type: "text"},
	}
	cfg.RPC = map[string]domain.Function{"f": fn}
	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected error for duplicate arg name")
	}
	assertHasErrorAt(t, errs, "rpc.f.args[1].name")
}

func TestValidate_IdentifierNaming(t *testing.T) {
	bad := []struct {
		kind string
		name string
	}{
		{"table", "product-items"},
		{"table", "Products"},
		{"table", "1things"},
		{"table", "_internal"},
		{"bucket", "my-bucket"},
		{"bucket", "MyBucket"},
		{"field", "First-Name"},
		{"function", "do-stuff"},
	}

	for _, tc := range bad {
		t.Run(tc.kind+"_"+tc.name, func(t *testing.T) {
			cfg := validBaseConfig()
			switch tc.kind {
			case "table":
				cfg.Tables[tc.name] = domain.Table{
					Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}},
				}
			case "bucket":
				cfg.Storage = map[string]domain.Bucket{tc.name: {Public: true}}
			case "field":
				cfg.Tables["items"] = domain.Table{
					Fields: []domain.Field{{Name: tc.name, Type: "text", PrimaryKey: true}},
				}
			case "function":
				fn := validRPCFunction()
				cfg.RPC = map[string]domain.Function{tc.name: fn}
			}
			errs := Validate(cfg)
			if errs == nil {
				t.Fatalf("expected error for %s %q", tc.kind, tc.name)
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e.Message, "invalid identifier") {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected 'invalid identifier' error for %s %q, got: %v", tc.kind, tc.name, errs)
			}
		})
	}
}

// SQL reserved words like `order`, `select`, `where` would pass identRE but
// blow up at migration time because we interpolate identifiers raw. Reject
// them at validate time across every YAML site that ends up as a SQL
// identifier (tables, columns, schemas, buckets, RPC functions, RPC args).
func TestValidate_ReservedSQLWord(t *testing.T) {
	bad := []struct {
		kind string
		name string
	}{
		{"table", "order"},
		{"table", "user"},
		{"field", "select"},
		{"field", "where"},
		{"field", "primary"},
		{"bucket", "table"},
		{"function", "group"},
	}

	for _, tc := range bad {
		t.Run(tc.kind+"_"+tc.name, func(t *testing.T) {
			cfg := validBaseConfig()
			switch tc.kind {
			case "table":
				cfg.Tables[tc.name] = domain.Table{
					Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}},
				}
			case "bucket":
				cfg.Storage = map[string]domain.Bucket{tc.name: {Public: true}}
			case "field":
				cfg.Tables["items"] = domain.Table{
					Fields: []domain.Field{
						{Name: "id", Type: "bigserial", PrimaryKey: true},
						{Name: tc.name, Type: "text"},
					},
				}
			case "function":
				cfg.RPC = map[string]domain.Function{tc.name: validRPCFunction()}
			}
			errs := Validate(cfg)
			if errs == nil {
				t.Fatalf("expected error for %s %q", tc.kind, tc.name)
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e.Message, "reserved SQL keyword") {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected 'reserved SQL keyword' error for %s %q, got: %v", tc.kind, tc.name, errs)
			}
		})
	}
}

// Schema names are interpolated into CREATE SCHEMA / GRANT statements, so
// they must clear the same identifier rules as tables.
func TestValidate_ReservedSchemaName(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.Schema = "user"
	cfg.Tables["todos"] = table

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.schema")
}

func TestValidate_BadSchemaName(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.Schema = "Bad-Schema"
	cfg.Tables["todos"] = table

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.schema")
}

// RPC arg name picks up reserved-word rejection too.
func TestValidate_ReservedRPCArgName(t *testing.T) {
	cfg := validBaseConfig()
	fn := validRPCFunction()
	fn.Args = []domain.FuncArg{{Name: "where", Type: "int"}}
	cfg.RPC = map[string]domain.Function{"f": fn}

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "rpc.f.args[0].name")
}

func TestValidateRejectsReservedSchemas(t *testing.T) {
	for _, reserved := range []string{"auth", "storage"} {
		cfg := &domain.Config{
			Version: 1,
			Tables: map[string]domain.Table{
				"sneaky": {Schema: reserved, Fields: []domain.Field{
					{Name: "id", Type: "BIGINT", PrimaryKey: true},
				}},
			},
		}
		errs := Validate(cfg)
		if errs == nil {
			t.Fatalf("schema=%q: expected validation error, got nil", reserved)
		}
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, reserved) || strings.Contains(e.Path, reserved) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("schema=%q: no error mentions the reserved schema name; errors: %v", reserved, errs)
		}
		assertHasErrorAt(t, errs, "tables.sneaky.schema")
	}
}

// --- Code Functions validation ---

func TestValidateCodeFunctions_Valid(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Functions = map[string]domain.CodeFunction{
		"send-welcome": {Runtime: "node", File: "functions/send-welcome.js", Timeout: "30s"},
	}
	if errs := Validate(cfg); errs != nil {
		t.Fatalf("expected valid, got %v", errs)
	}
}

func TestValidateCodeFunctions_ValidNoTimeout(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Functions = map[string]domain.CodeFunction{
		"my-fn": {Runtime: "node", File: "functions/my-fn.js"},
	}
	if errs := Validate(cfg); errs != nil {
		t.Fatalf("expected valid, got %v", errs)
	}
}

func TestValidateCodeFunctions_BadRuntime(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Functions = map[string]domain.CodeFunction{
		"x": {Runtime: "ruby", File: "functions/x.js"},
	}
	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected error for unsupported runtime")
	}
	assertHasErrorAt(t, errs, "functions.x.runtime")
}

func TestValidateCodeFunctions_MissingFile(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Functions = map[string]domain.CodeFunction{
		"x": {Runtime: "node"},
	}
	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected error for missing file")
	}
	assertHasErrorAt(t, errs, "functions.x.file")
}

func TestValidateCodeFunctions_InvalidName(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Functions = map[string]domain.CodeFunction{
		"bad name!": {Runtime: "node", File: "functions/x.js"},
	}
	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected error for invalid function name")
	}
	assertHasErrorAt(t, errs, "functions.bad name!")
}

func TestValidateCodeFunctions_BadTimeout(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Functions = map[string]domain.CodeFunction{
		"x": {Runtime: "node", File: "functions/x.js", Timeout: "soon"},
	}
	errs := Validate(cfg)
	if errs == nil {
		t.Fatal("expected error for unparseable timeout")
	}
	assertHasErrorAt(t, errs, "functions.x.timeout")
}

func TestRejectOldRPCShapeUnderFunctions(t *testing.T) {
	raw := []byte("version: 1\nfunctions:\n  legacy:\n    language: sql\n    body: \"SELECT 1\"\n    returns:\n      type: void\n")
	cfg, err := ParseBytes(raw, "test")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "functions.legacy")
}

func TestEmptyCodeFunctionReportsRequiredFields(t *testing.T) {
	cfg := &domain.Config{
		Version: 1,
		Functions: map[string]domain.CodeFunction{
			"my-fn": {},
		},
	}
	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "functions.my-fn")
}

func TestValidateProviders_ResendRequiresAPIKey(t *testing.T) {
	cfg := &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "test"},
		Providers: domain.Providers{
			Email: &domain.EmailProvider{Type: "resend"},
		},
	}
	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if e.Path == "providers.email.api_key" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected providers.email.api_key error, got: %v", errs)
	}
}

func TestValidateProviders_ResendWithAPIKey_Valid(t *testing.T) {
	cfg := &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "test"},
		Providers: domain.Providers{
			Email: &domain.EmailProvider{Type: "resend", APIKey: "${INSTANCEZ_RESEND_API_KEY}"},
		},
	}
	errs := Validate(cfg)
	for _, e := range errs {
		if e.Path == "providers.email.api_key" {
			t.Errorf("unexpected api_key error when set: %v", e.Message)
		}
	}
}

func TestValidateProviders_S3RequiresBucket(t *testing.T) {
	cfg := &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "test"},
		Providers: domain.Providers{
			Storage: &domain.StorageProvider{Type: "s3"},
		},
	}
	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if e.Path == "providers.storage.bucket" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected providers.storage.bucket error, got: %v", errs)
	}
}

func TestValidateProviders_S3WithBucket_Valid(t *testing.T) {
	cfg := &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "test"},
		Providers: domain.Providers{
			Storage: &domain.StorageProvider{Type: "s3", Bucket: "${INSTANCEZ_S3_BUCKET}"},
		},
	}
	errs := Validate(cfg)
	for _, e := range errs {
		if e.Path == "providers.storage.bucket" {
			t.Errorf("unexpected bucket error when set: %v", e.Message)
		}
	}
}

// Names that contain or extend a reserved word but aren't reserved
// themselves must still validate (regression guard).
func TestValidate_NonReservedSimilarNames(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Tables["orders"] = domain.Table{ // plural — not reserved
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "ordering", Type: "integer"},  // contains "order"
			{Name: "user_id", Type: "uuid"},      // contains "user"
			{Name: "selected", Type: "boolean"},  // contains "select"
		},
	}

	errs := Validate(cfg)
	if errs != nil {
		t.Fatalf("expected no errors for non-reserved names, got: %v", errs)
	}
}

// TestValidate_RPCBodyShape pins the rpc body contract: the migrator wraps
// the body in CREATE OR REPLACE FUNCTION itself, so a pasted full DDL
// statement is rejected.
func TestValidate_RPCBodyShape(t *testing.T) {
	withBody := func(body string) *domain.Config {
		cfg := validBaseConfig()
		cfg.RPC = map[string]domain.Function{
			"my_fn": {
				Language:   "sql",
				Volatility: "stable",
				Security:   "invoker",
				Returns:    domain.FuncReturn{Type: "void"},
				Body:       body,
			},
		}
		return cfg
	}

	for _, bad := range []string{
		"CREATE OR REPLACE FUNCTION my_fn() RETURNS void AS $x$ SELECT 1 $x$;",
		"create function my_fn() returns void as $x$ select 1 $x$;",
		"  \n-- comment\nCREATE OR REPLACE FUNCTION f() RETURNS void AS $x$ SELECT 1 $x$;",
	} {
		errs := Validate(withBody(bad))
		assertHasErrorAt(t, errs, "rpc.my_fn.body")
	}

	// Plain bodies — including ones that merely mention CREATE inside —
	// stay valid.
	for _, good := range []string{
		"SELECT 1",
		"BEGIN\n  EXECUTE 'CREATE TEMP TABLE t(x int)';\nEND;",
	} {
		for _, e := range Validate(withBody(good)) {
			if strings.HasPrefix(e.Path, "rpc.my_fn.body") {
				t.Fatalf("body %q should be valid, got %s: %s", good, e.Path, e.Message)
			}
		}
	}
}

// TestValidate_RLSCheckShape pins the RLS check contract: a boolean
// expression interpolated into CREATE POLICY ... USING (...). Statement
// separators and pasted DDL are rejected — a stray `;` would otherwise be
// spliced into the generated DDL verbatim.
func TestValidate_RLSCheckShape(t *testing.T) {
	withCheck := func(check string) *domain.Config {
		cfg := validBaseConfig()
		table := cfg.Tables["todos"]
		table.RLS = []domain.RLSPolicy{
			{Operations: []string{"select"}, Check: check},
		}
		cfg.Tables["todos"] = table
		return cfg
	}

	for _, bad := range []string{
		"user_id = auth.uid();",
		"true); DROP TABLE todos; --",
		"CREATE POLICY p ON todos USING (true)",
	} {
		errs := Validate(withCheck(bad))
		assertHasErrorAt(t, errs, "tables.todos.rls[0].check")
	}

	for _, good := range []string{
		"user_id = auth.uid()",
		"auth.is_authenticated() AND status = 'active'",
	} {
		for _, e := range Validate(withCheck(good)) {
			if strings.HasPrefix(e.Path, "tables.todos.rls[0].check") {
				t.Fatalf("check %q should be valid, got %s: %s", good, e.Path, e.Message)
			}
		}
	}
}
