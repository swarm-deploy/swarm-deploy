---
name: openapi
description: Rules for editing OpenAPI specifications
globs: ["api/**/*.yaml"]
apply: by file patterns
---

# Tools
- The project uses ogen to generate Go code from the OpenAPI specification.
- After each change to the OpenAPI specification, run `make gen` to regenerate the code.
