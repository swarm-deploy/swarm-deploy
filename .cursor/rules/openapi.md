---
name: openapi
description: Rules for writing code in Go
globs: ["api/*.yaml"]
apply: by file patterns
---

# Tools
- Project using code-generator ogen to generate go files from OpenAPI spec.
- After each change to the OpenAPI specification, run `make gen` to regenerate the code.
