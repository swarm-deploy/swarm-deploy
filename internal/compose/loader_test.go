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
			Title: "0. simple, without dependencies",
		},
		{
			Title: "1. with networks",
		},
		{
			Title: "2. with secrets and configs",
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

				fmt.Println(file.Compose.Services[0].Networks)
			}

			assert.Equal(t, string(file.RawBytes), result.String())
		})
	}
}
