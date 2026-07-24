package drive

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFcloneDirectoryLevels(t *testing.T) {
	got := fcloneDirectoryLevels([]string{
		"parent/child/grandchild",
		"sibling",
		"parent/child",
		"parent",
		"parent/child",
		"/another/child/",
		"",
	})
	assert.Equal(t, [][]string{
		{"parent", "sibling"},
		{"another/child", "parent/child"},
		{"parent/child/grandchild"},
	}, got)
}
