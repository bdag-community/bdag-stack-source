package gateway

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	etypes "github.com/ethereum/go-ethereum/core/types"
)

// PeekMethod attempts to extract the "method" field from a JSON-RPC request body
// without fully unmarshaling the structure. It returns the method string and nil error on success.
func PeekMethod(body []byte) (string, error) {
	const key = `"method"`
	idx := bytes.Index(body, []byte(key))
	if idx == -1 {
		return "", fmt.Errorf("method field not found")
	}
	// Move past "method"
	body = body[idx+len(key):]
	// Find colon
	colonIdx := bytes.IndexByte(body, ':')
	if colonIdx == -1 {
		return "", fmt.Errorf("malformed json")
	}
	body = body[colonIdx+1:]
	// Skip whitespace/quotes
	start := -1
	for i, b := range body {
		if b == '"' {
			start = i + 1
			break
		}
		if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
			continue
		}
	}
	if start == -1 || start >= len(body) {
		return "", fmt.Errorf("malformed json")
	}
	// Find end quote
	end := bytes.IndexByte(body[start:], '"')
	if end == -1 {
		return "", fmt.Errorf("malformed json")
	}
	return string(body[start : start+end]), nil
}

// ExtractSenderFromSendRawTx parses an eth_sendRawTransaction request and returns the sender address.
func ExtractSenderFromSendRawTx(body []byte) (string, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return "", fmt.Errorf("empty body")
	}
	if body[0] == '[' {
		return "", fmt.Errorf("batch request")
	}

	var req struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "", err
	}
	if req.Method != "eth_sendRawTransaction" {
		return "", fmt.Errorf("not sendRawTransaction")
	}
	if len(req.Params) == 0 {
		return "", fmt.Errorf("missing params")
	}

	var rawHex string
	if err := json.Unmarshal(req.Params[0], &rawHex); err != nil {
		return "", err
	}
	rawHex = strings.TrimPrefix(rawHex, "0x")
	if rawHex == "" {
		return "", fmt.Errorf("empty tx")
	}
	raw, err := hex.DecodeString(rawHex)
	if err != nil {
		return "", err
	}
	var tx etypes.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return "", err
	}
	signer := etypes.LatestSignerForChainID(tx.ChainId())
	from, err := etypes.Sender(signer, &tx)
	if err != nil {
		return "", err
	}
	return from.Hex(), nil
}

// ExtractAddressFromGetTransactionCount parses eth_getTransactionCount and returns the address.
func ExtractAddressFromGetTransactionCount(body []byte) (string, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return "", fmt.Errorf("empty body")
	}
	if body[0] == '[' {
		return "", fmt.Errorf("batch request")
	}

	var req struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "", err
	}
	if req.Method != "eth_getTransactionCount" {
		return "", fmt.Errorf("not getTransactionCount")
	}
	if len(req.Params) == 0 {
		return "", fmt.Errorf("missing params")
	}
	var addr string
	if err := json.Unmarshal(req.Params[0], &addr); err != nil {
		return "", err
	}
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", fmt.Errorf("empty address")
	}
	return addr, nil
}

// ExtractTxHashFromTxQuery parses tx lookup calls and returns the tx hash.
func ExtractTxHashFromTxQuery(body []byte) (string, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return "", fmt.Errorf("empty body")
	}
	if body[0] == '[' {
		return "", fmt.Errorf("batch request")
	}

	var req struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "", err
	}
	switch req.Method {
	case "eth_getTransactionReceipt", "eth_getTransactionByHash":
	default:
		return "", fmt.Errorf("not tx query")
	}
	if len(req.Params) == 0 {
		return "", fmt.Errorf("missing params")
	}
	var txHash string
	if err := json.Unmarshal(req.Params[0], &txHash); err != nil {
		return "", err
	}
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return "", fmt.Errorf("empty tx hash")
	}
	return txHash, nil
}

// writeRPCError crafts a JSON-RPC error response preserving the request IDs.
func (g *Gateway) writeRPCError(w http.ResponseWriter, r *http.Request, body []byte, err error) {
	ids := extractIDs(body)
	type rpcErr struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	responses := make([]rpcErr, 0, len(ids))
	for _, id := range ids {
		r := rpcErr{JSONRPC: "2.0", ID: id}
		r.Error.Code = -32000
		r.Error.Message = err.Error()
		responses = append(responses, r)
	}
	var out []byte
	if len(responses) == 1 {
		out, _ = json.Marshal(responses[0])
	} else {
		out, _ = json.Marshal(responses)
	}
	g.addCORSHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(out)
}

// extractIDs extracts JSON-RPC ids from the raw request body.
func extractIDs(body []byte) []json.RawMessage {
	body = bytes.TrimSpace(body)
	ids := []json.RawMessage{}
	if len(body) == 0 {
		return append(ids, json.RawMessage("null"))
	}
	if body[0] == '[' {
		var arr []json.RawMessage
		if json.Unmarshal(body, &arr) == nil {
			for _, item := range arr {
				var tmp struct {
					ID json.RawMessage `json:"id"`
				}
				if json.Unmarshal(item, &tmp) == nil {
					ids = append(ids, tmp.ID)
				}
			}
		}
	} else {
		var tmp struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(body, &tmp) == nil {
			ids = append(ids, tmp.ID)
		}
	}
	if len(ids) == 0 {
		ids = append(ids, json.RawMessage("null"))
	}
	return ids
}
