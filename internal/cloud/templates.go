package cloud

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/instancez/instancez/pkg/templates"
)

const MaxTemplateBytes = 2 << 20

type TemplateNotFoundError struct {
	ID          string
	Suggestions []string
}

func (e *TemplateNotFoundError) Error() string {
	if len(e.Suggestions) == 0 {
		return fmt.Sprintf("template %q not found", e.ID)
	}
	return fmt.Sprintf("template %q not found, did you mean: %s", templates.Clean(e.ID), strings.Join(e.Suggestions, ", "))
}

const maxErrorText = 200

// safeError flattens a server-influenced error into bounded, printable text.
func safeError(err error) error {
	msg := []rune(templates.Clean(err.Error()))
	if len(msg) > maxErrorText {
		msg = append(msg[:maxErrorText], '…')
	}
	return errors.New(string(msg))
}

func (c *Client) ListTemplates() ([]templates.Manifest, error) {
	cc := *c
	cc.Bearer, cc.Org, cc.MaxResponseBytes = "", "", MaxTemplateBytes
	var out struct {
		Templates []templates.Manifest `json:"templates"`
	}
	if err := cc.do("GET", "/public/templates", nil, &out); err != nil {
		return nil, safeError(err)
	}
	return out.Templates, nil
}

func (c *Client) GetTemplate(id string) (*templates.Bundle, error) {
	if !templates.ValidID(id) {
		return nil, fmt.Errorf("invalid template id %q", id)
	}
	cc := *c
	cc.Bearer, cc.Org, cc.MaxResponseBytes = "", "", MaxTemplateBytes
	var b templates.Bundle
	err := cc.do("GET", "/public/templates/"+url.PathEscape(id), nil, &b)
	var api *APIError
	if errors.As(err, &api) && api.Status == 404 {
		nf := &TemplateNotFoundError{ID: id, Suggestions: []string{}}
		var body struct {
			Suggestions []string `json:"suggestions"`
		}
		if json.Unmarshal([]byte(api.Body), &body) == nil && body.Suggestions != nil {
			for _, sg := range body.Suggestions {
				if templates.ValidID(sg) {
					nf.Suggestions = append(nf.Suggestions, sg)
				}
			}
		}
		return nil, nf
	}
	if err != nil {
		return nil, safeError(err)
	}
	if len(b.Files) == 0 {
		return nil, fmt.Errorf("template %q response has no files", id)
	}
	return &b, nil
}
