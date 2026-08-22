package postgres

// SetSQLEditorStatementTimeoutForTest overrides the editor statement timeout
// for the duration of a test; call the returned func to restore it.
func SetSQLEditorStatementTimeoutForTest(d string) func() {
	orig := sqlEditorStatementTimeout
	sqlEditorStatementTimeout = d
	return func() { sqlEditorStatementTimeout = orig }
}
