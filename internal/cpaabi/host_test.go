package cpaabi_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/magicvr/cpa-grok-panel/internal/cpaabi"
)

func TestHostGetAuthFileReadsJSONField(t *testing.T) {
	host := cpaabi.NewHost(func(method string, payload []byte) ([]byte, error) {
		if method != "host.auth.get" {
			t.Fatalf("method=%s", method)
		}
		var request map[string]string
		if err := json.Unmarshal(payload, &request); err != nil {
			t.Fatal(err)
		}
		if request["auth_index"] != "idx-1" {
			t.Fatalf("request=%s", payload)
		}
		return json.Marshal(map[string]any{
			"auth_index": "idx-1",
			"name":       "xai-a.json",
			"json":       map[string]any{"priority": -10, "disabled": false, "refresh_token": "keep-me"},
		})
	})

	document, err := host.GetAuthFile("idx-1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document, cpaabi.AuthDocument{"priority": float64(-10), "disabled": false, "refresh_token": "keep-me"}) {
		t.Fatalf("document=%#v", document)
	}
}

func TestHostGetAuthFileReadsStringEncodedJSON(t *testing.T) {
	host := cpaabi.NewHost(func(string, []byte) ([]byte, error) {
		return cpaabi.Success(map[string]any{
			"auth_index": "idx-1",
			"name":       "xai-a.json",
			"json":       `{"priority":7}`,
		}), nil
	})

	document, err := host.GetAuthFile("idx-1")
	if err != nil {
		t.Fatal(err)
	}
	if document["priority"] != float64(7) {
		t.Fatalf("document=%#v", document)
	}
}

func TestHostSaveAuthFileUsesJSONField(t *testing.T) {
	var captured map[string]json.RawMessage
	host := cpaabi.NewHost(func(method string, payload []byte) ([]byte, error) {
		if method != "host.auth.save" {
			t.Fatalf("method=%s", method)
		}
		if err := json.Unmarshal(payload, &captured); err != nil {
			t.Fatal(err)
		}
		return cpaabi.Success(map[string]any{}), nil
	})

	if err := host.SaveAuthFile("xai-a.json", cpaabi.AuthDocument{"priority": -100, "disabled": true}); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := json.Unmarshal(captured["name"], &name); err != nil || name != "xai-a.json" {
		t.Fatalf("name=%q err=%v", name, err)
	}
	if _, exists := captured["auth"]; exists {
		t.Fatalf("unexpected auth field: %s", captured["auth"])
	}
	var document cpaabi.AuthDocument
	if err := json.Unmarshal(captured["json"], &document); err != nil {
		t.Fatal(err)
	}
	if document["priority"] != float64(-100) || document["disabled"] != true {
		t.Fatalf("json=%#v", document)
	}
}
