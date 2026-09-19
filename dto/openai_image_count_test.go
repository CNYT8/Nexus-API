package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestImageBillingJSONQuantity(t *testing.T) {
	for _, tc := range []struct {
		body string
		want int
		bad  bool
	}{
		{`{}`, 1, false}, {`{"n":0}`, 1, false}, {`{"n":null}`, 1, false},
		{`{"n":2,"parameters":{}}`, 2, false}, {`{"n":2,"parameters":{"n":null}}`, 2, false},
		{`{"n":2,"parameters":{"n":3}}`, 3, false}, {`{"parameters":{"n":128}}`, 128, false},
		{`{"n":129}`, 0, true}, {`{"n":-1}`, 0, true}, {`{"n":1.5}`, 0, true},
		{`{"n":"2"}`, 0, true}, {`{"n":18446744073709551616}`, 0, true},
		{`{"parameters":{"n":0}}`, 0, true}, {`{"parameters":{"n":-1}}`, 0, true},
		{`{"parameters":{"n":129}}`, 0, true}, {`{"parameters":{"n":1.5}}`, 0, true},
		{`{"parameters":{"n":18446744073709551616}}`, 0, true},
		{`{"parameters":{"prompt_extend":"true"}}`, 0, true},
		{`{"parameters":[]}`, 0, true}, {`null`, 0, true}, {`[]`, 0, true},
		{`{"n":2} garbage`, 0, true}, {`{"n":2`, 0, true}, {``, 0, true},
	} {
		t.Run(tc.body, func(t *testing.T) {
			req, err := ImageBillingRequestFromJSON([]byte(tc.body))
			var n int
			if err == nil {
				n, err = req.ImageCount(true)
			}
			if tc.bad {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, n)
			}
		})
	}
}

func TestImageBillingProviderPrecedenceAndSerialization(t *testing.T) {
	var req ImageRequest
	require.NoError(t, common.Unmarshal([]byte(`{"model":"z-image","n":2,"parameters":{"n":3,"prompt_extend":false}}`), &req))
	n, err := req.ImageCount(true)
	require.NoError(t, err)
	require.Equal(t, 3, n)
	n, err = req.ImageCount(false)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	copy, err := common.DeepCopy(&req)
	require.NoError(t, err)
	n, err = copy.ImageCount(true)
	require.NoError(t, err)
	require.Equal(t, 3, n)
	// Nexus intentionally does not forward arbitrary Extra fields on OpenAI.
	encoded, err := common.Marshal(copy)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "parameters")
	require.NotContains(t, string(encoded), "BillingParameters")
}

func TestImageBillingMultipartScalars(t *testing.T) {
	for _, fields := range []map[string][]string{
		{"n": {"2", "3"}}, {"parameters": {`{}`, `{}`}},
		{"n": {""}}, {"n": {"1.5"}}, {"n": {"-1"}}, {"n": {"129"}},
		{"parameters": {""}}, {"parameters": {`{"n":0}`}},
	} {
		req, err := ImageBillingRequestFromForm(fields)
		if err == nil {
			_, err = req.ImageCount(true)
		}
		require.Error(t, err, "%v", fields)
	}
}
