package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/httpapi"
)

func TestBoardAutomodRulesHTTP(t *testing.T) {
	c := newHTTPTestCore(t)
	h := httpapi.New(c, []byte("test-secret")).Handler()

	do := func(method, path, token string, body any) *httptest.ResponseRecorder {
		var rdr *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		} else {
			rdr = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, rdr)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	topField := func(rec *httptest.ResponseRecorder, key string) string {
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		s, _ := m[key].(string)
		return s
	}

	reg := do(http.MethodPost, "/api/v1/auth/register", "", map[string]string{"name": "boss", "password": "password123"})
	token := topField(reg, "token")
	if token == "" {
		t.Fatalf("register: %d %s", reg.Code, reg.Body.String())
	}

	cmd := func(command string, payload any) *httptest.ResponseRecorder {
		return do(http.MethodPost, "/api/v1/commands", token, map[string]any{"command": command, "payload": payload})
	}

	if r := cmd("createBoard", map[string]any{"id": "lounge", "name": "Lounge"}); r.Code != http.StatusOK && r.Code != http.StatusCreated {
		t.Fatalf("createBoard: %d %s", r.Code, r.Body.String())
	}

	setRec := cmd("setBoardAutomodRule", map[string]any{
		"board": "lounge", "matchType": "keyword", "pattern": "spam", "action": "manual_review", "reason": "no spam",
	})
	if setRec.Code != http.StatusOK && setRec.Code != http.StatusCreated {
		t.Fatalf("setBoardAutomodRule: %d %s", setRec.Code, setRec.Body.String())
	}
	var ack struct {
		Result struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	_ = json.Unmarshal(setRec.Body.Bytes(), &ack)
	if ack.Result.ID == "" {
		t.Fatalf("no rule id in ack: %s", setRec.Body.String())
	}

	list := do(http.MethodGet, "/api/v1/boards/lounge/automod-rules", token, nil)
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte(`"pattern":"spam"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"action":"manual_review"`)) {
		t.Fatalf("list rules: %d %s", list.Code, list.Body.String())
	}

	if r := cmd("deleteBoardAutomodRule", map[string]any{"board": "lounge", "id": ack.Result.ID}); r.Code != http.StatusOK && r.Code != http.StatusCreated {
		t.Fatalf("deleteBoardAutomodRule: %d %s", r.Code, r.Body.String())
	}
	after := do(http.MethodGet, "/api/v1/boards/lounge/automod-rules", token, nil)
	if after.Code != http.StatusOK || !bytes.Contains(after.Body.Bytes(), []byte(`"rules":[]`)) {
		t.Fatalf("expected empty rules after delete: %d %s", after.Code, after.Body.String())
	}
}
