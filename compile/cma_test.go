package compile

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCMACommitteeCompatibility(t *testing.T) {
	for _, tc := range []struct{ raw, tag string }{
		{`["abcd"]`, "legacy"},
		{`[{"tag":"schnorr","value":"abcd"}]`, "schnorr"},
		{`[{"tag":"ecdsa","value":"abcd"}]`, "ecdsa"},
		{`[{"tag":"future","value":"abcd"}]`, "future"},
	} {
		var committee CMACommittee
		require.NoError(t, json.Unmarshal([]byte(tc.raw), &committee))
		require.Equal(t, tc.tag, committee[0].Tag)
		require.Equal(t, []string{"abcd"}, committee.Values())
		encoded, err := json.Marshal(committee)
		require.NoError(t, err)
		var saved CMACommittee
		require.NoError(t, json.Unmarshal(encoded, &saved))
		require.Equal(t, committee, saved)
	}
	for _, raw := range []string{`[null]`, `[1]`, `[{}]`, `[{"tag":"schnorr"}]`, `{"committee":[]}`, `["" ]`} {
		var committee CMACommittee
		require.Error(t, json.Unmarshal([]byte(raw), &committee), raw)
	}
}
