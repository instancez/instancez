package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckFunctionImports(t *testing.T) {
	ok := map[string]string{
		"functions/a.js":     `import x from "./lib/b.js"; import y from "@sb/x"; import z from "node:fs"`,
		"functions/lib/b.js": `export default 1`,
	}
	assert.Empty(t, CheckFunctionImports(ok))
	assert.Empty(t, CheckFunctionImports(nil))
	assert.Empty(t, CheckFunctionImports(map[string]string{}))

	missing := map[string]string{"functions/a.js": `import x from "./lib/b.js"`}
	probs := CheckFunctionImports(missing)
	assert.Len(t, probs, 1)
	assert.Contains(t, probs[0], "functions/a.js")

	commented := map[string]string{"functions/a.js": "// import x from \"./gone.js\"\n/* import y from \"./gone2.js\" */"}
	assert.Empty(t, CheckFunctionImports(commented))

	nonJS := map[string]string{"functions/a.json": `{"from": "./nope.js"}`}
	assert.Empty(t, CheckFunctionImports(nonJS))

	nested := map[string]string{"functions/lib/b.js": `import x from "../a.js"`, "functions/a.js": ""}
	assert.Empty(t, CheckFunctionImports(nested))
}
