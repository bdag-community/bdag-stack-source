package cmds

import (
	"encoding/json"
	"testing"
)

func TestGetChainIdentityCommandIsZeroParameter(t *testing.T) {
	raw, err := MarshalCmd(1, NewGetChainIdentityCmd())
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	if request.Method != "Blockdag_getChainIdentity" {
		t.Fatalf("method = %q, want Blockdag_getChainIdentity", request.Method)
	}
	if len(request.Params) != 0 {
		t.Fatalf("params = %v, want zero parameters", request.Params)
	}
}
