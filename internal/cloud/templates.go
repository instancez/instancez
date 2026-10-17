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

// safeError flattens a server-influenced error into bounded, printable text.
func safeError(err error) error {
	return errors.New(templates.CleanN(err.Error(), 200))
}

func (c *Client) public() *Client {
	cc := *c
	cc.Bearer, cc.Org, cc.MaxResponseBytes = "", "", MaxTemplateBytes
	return &cc
}

func (c *Client) ListTemplates() ([]templates.Manifest, error) {
	var out struct {
		Templates []templates.Manifest `json:"templates"`
	}
	if err := c.public().do("GET", "/public/templates", nil, &out); err != nil {
		return nil, safeError(err)
	}
	valid := out.Templates[:0]
	for _, m := range out.Templates {
		if templates.ValidID(m.ID) {
			valid = append(valid, m)
		}
	}
	return valid, nil
}

func (c *Client) GetTemplate(id string) (*templates.Bundle, error) {
	if !templates.ValidID(id) {
		return nil, fmt.Errorf("invalid template id %q", id)
	}
	var b templates.Bundle
	err := c.public().do("GET", "/public/templates/"+url.PathEscape(id), nil, &b)
	var api *APIError
	if errors.As(err, &api) && api.Status == 404 {
		nf := &TemplateNotFoundError{ID: id, Suggestions: []string{}}
		var body struct {
			Suggestions []string `json:"suggestions"`
		}
		if json.Unmarshal([]byte(api.Body), &body) == nil {
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
	return &b, nil
}
