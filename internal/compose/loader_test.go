package compose

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLoader_Load(t *testing.T) {
	cases := []struct {
		Title string
	}{
		{
			Title: "0. simple, without dependencies, service ports: mappings, labels: mappings",
		},
		{
			Title: "1. with networks, service ports: sequence, labels: sequence",
		},
		{
			Title: "2. with secrets and configs, volumes: sequence",
		},
	}

	for i, test := range cases {
		t.Run(test.Title, func(t *testing.T) {
			loader := NewFileLoader()

			file, err := loader.Load(fmt.Sprintf("./tests/loader/%d.input.yaml", i))
			require.NoError(t, err)

			result := bytes.NewBuffer(nil)
			encoder := yaml.NewEncoder(result)
			encoder.SetIndent(2)
			err = encoder.Encode(file.Compose)
			require.NoError(t, err)

			if string(file.RawBytes) != result.String() {
				err = os.WriteFile(fmt.Sprintf("./tests/loader/%d.actual.yaml", i), result.Bytes(), 0666)
				require.NoError(t, err)
			}

			assert.Equal(t, string(file.RawBytes), result.String())
		})
	}
}
