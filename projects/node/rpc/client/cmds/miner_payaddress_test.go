package cmds

import (
	"encoding/json"
	"testing"
)

func TestTemplateOptionalPaymentParameter(t *testing.T) {
	cmd := NewGetBlockTemplateCmd([]string{"proposal"}, 10)
	raw, err := MarshalCmd(1, cmd)
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Params []json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Params) != 2 {
		t.Fatal("legacy default request changed")
	}
	address := "0x0000000000000000000000000000000000000001"
	cmd.PayAddress = &address
	raw, err = MarshalCmd(1, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Params) != 3 || string(req.Params[2]) != `"`+address+`"` {
		t.Fatalf("explicit payment parameter missing: %s", raw)
	}
}
