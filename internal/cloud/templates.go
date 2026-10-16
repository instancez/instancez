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
	return fmt.Sprintf("template %q not found, did you mean: %s", e.ID, strings.Join(e.Suggestions, ", "))
}

func (c *Client) ListTemplates() ([]templates.Manifest, error) {
	cc := *c
	cc.Bearer, cc.MaxResponseBytes = "", MaxTemplateBytes
	var out struct {
		Templates []templates.Manifest `json:"templates"`
	}
	if err := cc.do("GET", "/public/templates", nil, &out); err != nil {
		return nil, err
	}
	return out.Templates, nil
}

func (c *Client) GetTemplate(id string) (*templates.Bundle, error) {
	if !templates.ValidID(id) {
		return nil, fmt.Errorf("invalid template id %q", id)
	}
	cc := *c
	cc.Bearer, cc.MaxResponseBytes = "", MaxTemplateBytes
	var b templates.Bundle
	err := cc.do("GET", "/public/templates/"+url.PathEscape(id), nil, &b)
	var api *APIError
	if errors.As(err, &api) && api.Status == 404 {
		nf := &TemplateNotFoundError{ID: id, Suggestions: []string{}}
		var body struct {
			Suggestions []string `json:"suggestions"`
		}
		if json.Unmarshal([]byte(api.Body), &body) == nil && body.Suggestions != nil {
			nf.Suggestions = body.Suggestions
		}
		return nil, nf
	}
	if err != nil {
		return nil, err
	}
	if len(b.Files) == 0 {
		return nil, fmt.Errorf("template %q response has no files", id)
	}
	return &b, nil
}
