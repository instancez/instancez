package http

import "github.com/instancez/instancez/internal/domain"

// defaultEmailTemplates are the built-in auth emails, used whenever
// auth.email.templates does not override a kind's subject/body.
// Bodies are rendered by renderAuthTemplate; supported vars per kind:
//
//	verification: {{link}} {{token}} {{email}} {{base_url}}
//	magiclink:    {{link}} {{code}} {{token}} {{email}} {{base_url}}
//	reset:        {{link}} {{token}} {{email}} {{base_url}}
var defaultEmailTemplates = map[string]domain.EmailTemplate{
	"verification": {
		Subject: "Confirm your email",
		Body: `Hi,

Thanks for signing up. Confirm your email address by clicking the link below:

{{link}}

If you didn't create an account, you can safely ignore this email.`,
	},
	"magiclink": {
		Subject: "Your sign-in link",
		Body: `Hi,

Click the link below to sign in:

{{link}}

Or enter this one-time code: {{code}}

If you didn't request this, you can safely ignore this email.`,
	},
	"reset": {
		Subject: "Reset your password",
		Body: `Hi,

We received a request to reset the password for {{email}}.

Reset it by clicking the link below:

{{link}}

If you didn't request a reset, you can safely ignore this email — your password is unchanged.`,
	},
}

// resolveEmailTemplate merges a configured override (if any) over the
// built-in default for the kind and renders the body with vars.
func (h *AuthHandler) resolveEmailTemplate(name string, vars map[string]string) (subject, body string) {
	tmpl := defaultEmailTemplates[name]
	if h.cfg.Auth != nil && h.cfg.Auth.Email != nil {
		if custom, ok := h.cfg.Auth.Email.Templates[name]; ok {
			if custom.Subject != "" {
				tmpl.Subject = custom.Subject
			}
			if custom.Body != "" {
				tmpl.Body = custom.Body
			}
		}
	}
	return tmpl.Subject, renderAuthTemplate(tmpl.Body, vars)
}
