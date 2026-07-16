package cloud

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBrowserCommandForOS(t *testing.T) {
	tests := []struct {
		goos string
		want string
	}{
		{"linux", "xdg-open"},
		{"darwin", "open"},
		{"windows", "rundll32"},
		{"freebsd", ""},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			assert.Equal(t, tt.want, browserCommand(tt.goos))
		})
	}
}
