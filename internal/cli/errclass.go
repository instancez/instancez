package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"strings"
	"syscall"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/cloud"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/telemetry"
	"github.com/jackc/pgx/v5/pgconn"
)

type reportedErr struct{ class, detail string }

func (e *reportedErr) Error() string { return errReported.Error() }

func (e *reportedErr) Is(target error) bool { return target == errReported }

// reported marks an already-printed error and keeps its class for telemetry.
func reported(class string) error { return &reportedErr{class: class} }

// reportedValidation is reported("config_invalid") carrying a validation summary for telemetry.
func reportedValidation(errs domain.ValidationErrors) error {
	return &reportedErr{class: "config_invalid", detail: validationSummary(errs)}
}

// validationSummary is the first three errors as "path: message", each sanitized.
func validationSummary(errs domain.ValidationErrors) string {
	parts := make([]string, 0, 3)
	for _, e := range errs[:min(3, len(errs))] {
		if e != nil {
			parts = append(parts, telemetry.Sanitize(e.Path+": "+e.Message, teleEnv{}.homeDir()))
		}
	}
	return strings.Join(parts, "; ")
}

type userErr struct {
	class string
	err   error
}

func (e *userErr) Error() string { return e.err.Error() }

func (e *userErr) Unwrap() error { return e.err }

// userError marks a user mistake, keeping its message and giving it a telemetry class.
func userError(class string, err error) error { return &userErr{class: class, err: err} }

// classifyErr returns a stable class, whether the error is a user mistake, and its message.
func classifyErr(err error) (class string, isExpected bool, msg string) {
	if err == nil {
		return "", false, ""
	}
	msg = err.Error()

	var rep *reportedErr
	if errors.As(err, &rep) {
		if rep.detail != "" {
			msg = rep.detail
		}
		return rep.class, true, msg
	}
	var ue *userErr
	if errors.As(err, &ue) {
		return ue.class, true, msg
	}
	if errors.Is(err, errReported) {
		return "reported", true, msg
	}
	if errors.Is(err, app.ErrDestructive) {
		return "migrate:destructive", true, msg
	}

	var cfgErr *domain.ConfigError
	if errors.As(err, &cfgErr) {
		if errors.Is(err, fs.ErrNotExist) {
			return "config_missing", true, msg
		}
		return "config_parse", true, msg
	}
	var envErr *domain.MissingEnvError
	var valErrs domain.ValidationErrors
	if errors.As(err, &valErrs) && len(valErrs) > 0 {
		return "config_invalid", true, validationSummary(valErrs)
	}
	if errors.As(err, &envErr) || errors.As(err, &valErrs) {
		return "config_invalid", true, msg
	}

	var apiErr *cloud.APIError
	if errors.As(err, &apiErr) {
		return cloudAPIClass(apiErr.Status), true, msg
	}

	var dnsErr *net.DNSError
	var connErr *pgconn.ConnectError
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &connErr) && errors.As(err, &pgErr) && (pgErr.Code == "28P01" || pgErr.Code == "28000"):
		return "env:db_auth", true, msg
	case errors.As(err, &connErr):
		return "env:db_connect", true, msg
	case errors.Is(err, syscall.ECONNREFUSED):
		return "env:connect_refused", true, msg
	case errors.Is(err, syscall.EADDRINUSE):
		return "env:port_in_use", true, msg
	case errors.As(err, &dnsErr):
		return "env:dns", true, msg
	}

	// cobra and pflag return untyped errors for bad commands and flags, so match their text.
	switch {
	case strings.HasPrefix(msg, "unknown command"):
		return "cli_unknown_command", true, msg
	case hasAnyPrefix(msg, "accepts ", "requires at least "):
		return "cli_args", true, msg
	case hasAnyPrefix(msg, "unknown flag", "unknown shorthand flag", "flag needs an argument", "invalid argument"):
		return "cli_flag", true, msg
	}

	inner := err
	for {
		var next error
		switch u := inner.(type) {
		case interface{ Unwrap() []error }:
			if errs := u.Unwrap(); len(errs) > 0 {
				next = errs[0]
			}
		case interface{ Unwrap() error }:
			next = u.Unwrap()
		}
		if next == nil {
			break
		}
		inner = next
	}
	return fmt.Sprintf("go:%T", inner), false, msg
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func cloudAPIClass(status int) string {
	if status <= 0 {
		return "cloud_api"
	}
	return fmt.Sprintf("cloud_api:%d", status)
}
